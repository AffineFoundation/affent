package sessionsearch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/affinefoundation/affent/internal/jsonl"
	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/planstate"
	"github.com/affinefoundation/affent/internal/textutil"
)

// Hit is one matched message from a past session.
type Hit struct {
	SessionID       string   `json:"session_id"`
	TurnIdx         int      `json:"turn_idx"`
	MessageIdx      int      `json:"message_idx,omitempty"`
	Role            string   `json:"role"`
	Snippet         string   `json:"snippet"`
	Score           float64  `json:"score"`
	MatchedTerms    []string `json:"matched_terms,omitempty"`
	ContextIncluded bool     `json:"context_included,omitempty"`
	ModTime         string   `json:"mod_time,omitempty"`
}

// RecentSession is a bounded no-match recovery hint. It gives the agent a few
// fresh transcript/task-state anchors without returning full historical
// conversations.
type RecentSession struct {
	SessionID       string `json:"session_id"`
	ModTime         string `json:"mod_time,omitempty"`
	LatestUser      string `json:"latest_user,omitempty"`
	LatestAssistant string `json:"latest_assistant,omitempty"`
	Plan            string `json:"plan,omitempty"`
	Loop            string `json:"loop,omitempty"`
	Recovery        string `json:"recovery,omitempty"`
}

// Response is the session_search tool return shape.
type Response struct {
	Query          string          `json:"query"`
	Total          int             `json:"total"`
	Results        []Hit           `json:"results"`
	Message        string          `json:"message,omitempty"`
	RecentSessions []RecentSession `json:"recent_sessions,omitempty"`
}

// Search scans sessionsDir/*.jsonl for messages matching query. The
// current session is excluded. Scoring is lexical term overlap, which
// is enough for local recall over one workspace's transcript history.
const (
	DefaultTopK           = 5
	MaxTopK               = 20
	DefaultMaxPerSession  = 3
	MaxPerSession         = 5
	MaxQueryBytes         = 2048
	MaxQueryTerms         = 16
	DefaultRecentSessions = 5
	MaxRecentSessions     = 8

	sessionDirReadBatch       = 128
	maxSessionLogLineBytes    = jsonl.DefaultMaxRecordBytes
	recentSessionPreviewBytes = 220
)

func Search(ctx context.Context, sessionsDir, currentSessionID, query string, topK, maxPerSession int) ([]Hit, error) {
	topK, maxPerSession = NormalizeLimits(topK, maxPerSession)
	terms := Tokenize(query)
	if len(terms) == 0 {
		return nil, nil
	}
	dir, err := os.Open(sessionsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer dir.Close()
	var all []Hit
	for {
		entries, rerr := dir.ReadDir(sessionDirReadBatch)
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return nil, rerr
		}
		for _, ent := range entries {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if ent.IsDir() {
				sid := ent.Name()
				if sid == currentSessionID || entryIsSymlink(ent) {
					continue
				}
				var sessionHits []Hit
				path := filepath.Join(sessionsDir, sid, "conversation.jsonl")
				if mtime, ok := regularFileModTime(path); ok {
					hits, serr := scoreFile(ctx, path, sid, terms, maxPerSession, mtime)
					if serr != nil {
						if ctx.Err() != nil {
							return nil, ctx.Err()
						}
					} else {
						for _, hit := range hits {
							sessionHits = appendBoundedHits(sessionHits, hit, maxPerSession)
						}
					}
				}
				planPath := filepath.Join(sessionsDir, sid, "plan.json")
				if mtime, ok := regularFileModTime(planPath); ok {
					hit, ok, serr := scorePlanFile(ctx, planPath, sid, terms, mtime)
					if serr != nil {
						if ctx.Err() != nil {
							return nil, ctx.Err()
						}
					} else if ok {
						sessionHits = appendBoundedHits(sessionHits, hit, maxPerSession)
					}
				}
				loopPath := loopstate.ProtocolPath(filepath.Join(sessionsDir, sid), sid)
				loopStatePath := filepath.Join(filepath.Dir(loopPath), loopstate.StateFileName)
				if mtime, ok := newestRegularFileModTime(loopPath, loopStatePath); ok {
					hit, ok, serr := scoreLoopFile(ctx, loopPath, sid, terms, mtime)
					if serr != nil {
						if ctx.Err() != nil {
							return nil, ctx.Err()
						}
					} else if ok {
						sessionHits = appendBoundedHits(sessionHits, hit, maxPerSession)
					}
				}
				eventsPath := filepath.Join(sessionsDir, sid, "events.jsonl")
				if mtime, ok := regularFileModTime(eventsPath); ok {
					hits, serr := scoreEventsFile(ctx, eventsPath, sid, terms, maxPerSession, mtime)
					if serr != nil {
						if ctx.Err() != nil {
							return nil, ctx.Err()
						}
					} else {
						for _, hit := range hits {
							sessionHits = appendBoundedHits(sessionHits, hit, maxPerSession)
						}
					}
				}
				for _, hit := range sessionHits {
					all = appendBoundedHits(all, hit, topK)
				}
				continue
			}
			if !strings.HasSuffix(ent.Name(), ".jsonl") {
				continue
			}
			if entryIsSymlink(ent) {
				continue
			}
			sid := strings.TrimSuffix(ent.Name(), ".jsonl")
			if sid == currentSessionID {
				continue
			}
			info, ierr := ent.Info()
			mtime := ""
			if ierr == nil {
				mtime = info.ModTime().UTC().Format(time.RFC3339)
			}
			hits, serr := scoreFile(ctx, filepath.Join(sessionsDir, ent.Name()), sid, terms, maxPerSession, mtime)
			if serr != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue
			}
			for _, hit := range hits {
				all = appendBoundedHits(all, hit, topK)
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
	}
	sortHits(all)
	if topK > 0 && len(all) > topK {
		all = all[:topK]
	}
	return all, nil
}

type sessionLogCandidate struct {
	sessionID  string
	path       string
	planPath   string
	loopPath   string
	eventsPath string
	modTime    time.Time
}

// RecentSessions returns recent transcript summaries to help recover from a
// failed lexical search. The current session is excluded.
func RecentSessions(ctx context.Context, sessionsDir, currentSessionID string, limit int) ([]RecentSession, error) {
	if limit <= 0 {
		limit = DefaultRecentSessions
	}
	if limit > MaxRecentSessions {
		limit = MaxRecentSessions
	}
	dir, err := os.Open(sessionsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer dir.Close()
	var candidates []sessionLogCandidate
	for {
		entries, rerr := dir.ReadDir(sessionDirReadBatch)
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return nil, rerr
		}
		for _, ent := range entries {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if ent.IsDir() {
				sid := ent.Name()
				if sid == currentSessionID || entryIsSymlink(ent) {
					continue
				}
				conversationPath := filepath.Join(sessionsDir, sid, "conversation.jsonl")
				planPath := filepath.Join(sessionsDir, sid, "plan.json")
				loopPath := loopstate.ProtocolPath(filepath.Join(sessionsDir, sid), sid)
				loopStatePath := filepath.Join(filepath.Dir(loopPath), loopstate.StateFileName)
				eventsPath := filepath.Join(sessionsDir, sid, "events.jsonl")
				conversationInfo, hasConversation := regularFileInfo(conversationPath)
				planInfo, hasPlan := regularFileInfo(planPath)
				loopInfo, hasLoop := regularFileInfo(loopPath)
				loopStateInfo, hasLoopState := regularFileInfo(loopStatePath)
				eventsInfo, hasEvents := regularFileInfo(eventsPath)
				if !hasConversation && !hasPlan && !hasLoop && !hasLoopState && !hasEvents {
					continue
				}
				modTime := time.Time{}
				if hasConversation {
					modTime = conversationInfo.ModTime()
				}
				if hasPlan && (modTime.IsZero() || planInfo.ModTime().After(modTime)) {
					modTime = planInfo.ModTime()
				}
				if hasLoop && (modTime.IsZero() || loopInfo.ModTime().After(modTime)) {
					modTime = loopInfo.ModTime()
				}
				if hasLoopState && (modTime.IsZero() || loopStateInfo.ModTime().After(modTime)) {
					modTime = loopStateInfo.ModTime()
				}
				if hasEvents && (modTime.IsZero() || eventsInfo.ModTime().After(modTime)) {
					modTime = eventsInfo.ModTime()
				}
				candidates = append(candidates, sessionLogCandidate{
					sessionID:  sid,
					path:       conversationPath,
					planPath:   planPath,
					loopPath:   loopPath,
					eventsPath: eventsPath,
					modTime:    modTime,
				})
				continue
			}
			if !strings.HasSuffix(ent.Name(), ".jsonl") || entryIsSymlink(ent) {
				continue
			}
			sid := strings.TrimSuffix(ent.Name(), ".jsonl")
			if sid == currentSessionID {
				continue
			}
			info, ierr := ent.Info()
			if ierr != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			candidates = append(candidates, sessionLogCandidate{
				sessionID: sid,
				path:      filepath.Join(sessionsDir, ent.Name()),
				modTime:   info.ModTime(),
			})
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if !candidates[i].modTime.Equal(candidates[j].modTime) {
			return candidates[i].modTime.After(candidates[j].modTime)
		}
		return candidates[i].sessionID < candidates[j].sessionID
	})
	out := make([]RecentSession, 0, limit)
	for _, cand := range candidates {
		if len(out) >= limit {
			break
		}
		summary, ok, err := recentSessionSummary(ctx, cand)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		if !ok {
			continue
		}
		out = append(out, summary)
	}
	return out, nil
}

func recentSessionSummary(ctx context.Context, cand sessionLogCandidate) (RecentSession, bool, error) {
	if err := ctx.Err(); err != nil {
		return RecentSession{}, false, err
	}
	summary := RecentSession{
		SessionID: cand.sessionID,
		ModTime:   cand.modTime.UTC().Format(time.RFC3339),
	}
	if cand.path != "" {
		if _, ok := regularFileInfo(cand.path); !ok {
			if cand.planPath == "" && cand.loopPath == "" && cand.eventsPath == "" {
				return RecentSession{}, false, errors.New("session log must be a regular file")
			}
		} else {
			f, err := os.Open(cand.path)
			if err != nil {
				return RecentSession{}, false, err
			}
			defer f.Close()
			reader := bufio.NewReaderSize(f, 64*1024)
			for {
				line, overLimit, err := jsonl.ReadBoundedLine(reader, maxSessionLogLineBytes)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return RecentSession{}, false, err
				}
				if err := ctx.Err(); err != nil {
					return RecentSession{}, false, err
				}
				if overLimit {
					continue
				}
				var m struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				}
				if err := json.Unmarshal(line, &m); err != nil {
					continue
				}
				preview := recentSessionPreview(m.Content)
				if preview == "" {
					continue
				}
				switch m.Role {
				case "user":
					summary.LatestUser = preview
				case "assistant":
					summary.LatestAssistant = preview
				}
			}
		}
	}
	if cand.planPath != "" {
		if plan := recentPlanPreview(cand.planPath); plan != "" {
			summary.Plan = plan
		}
	}
	if cand.loopPath != "" {
		if loop := recentLoopPreview(cand.loopPath, cand.sessionID); loop != "" {
			summary.Loop = loop
		}
	}
	if cand.eventsPath != "" {
		if recovery := recentRecoveryPreview(cand.eventsPath); recovery != "" {
			summary.Recovery = recovery
		}
	}
	if summary.LatestUser == "" && summary.LatestAssistant == "" && summary.Plan == "" && summary.Loop == "" && summary.Recovery == "" {
		return RecentSession{}, false, nil
	}
	return summary, true, nil
}

func recentPlanPreview(path string) string {
	summary, found := planstate.SummarizeFile(path)
	if !found || summary.Error || summary.Label == planstate.LabelMissing || summary.Label == planstate.LabelEmpty || summary.Label == planstate.LabelError {
		return ""
	}
	return recentSessionPreview(planSearchContent(summary))
}

func recentSessionPreview(s string) string {
	s = textutil.StripASCIIControls(s)
	s = textutil.CompactWhitespace(s)
	return textutil.Preview(s, recentSessionPreviewBytes)
}

func NormalizeLimits(topK, maxPerSession int) (int, int) {
	if topK <= 0 {
		topK = DefaultTopK
	}
	if topK > MaxTopK {
		topK = MaxTopK
	}
	if maxPerSession <= 0 {
		maxPerSession = DefaultMaxPerSession
	}
	if maxPerSession > MaxPerSession {
		maxPerSession = MaxPerSession
	}
	return topK, maxPerSession
}

func scoreFile(ctx context.Context, path, sid string, terms []string, maxPerSession int, mtime string) ([]Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("session log must be a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 64*1024)
	var fileHits []Hit
	messageIdx := 0
	turnIdx := 0
	var prev searchableMessage
	var pendingUser *pendingUserHit
	var latestMetaHit Hit
	for {
		line, overLimit, err := jsonl.ReadBoundedLine(reader, maxSessionLogLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		messageIdx++
		flushPendingUser := func() {
			if pendingUser == nil {
				return
			}
			fileHits = appendBoundedHits(fileHits, pendingUser.hit, maxPerSession)
			pendingUser = nil
		}
		if overLimit {
			flushPendingUser()
			prev = searchableMessage{}
			continue
		}
		var m struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(line, &m); err != nil {
			flushPendingUser()
			prev = searchableMessage{}
			continue
		}
		if m.Role != "user" && m.Role != "assistant" {
			flushPendingUser()
			prev = searchableMessage{}
			continue
		}
		if pendingUser != nil && m.Role != "assistant" {
			flushPendingUser()
		}
		if m.Role == "user" {
			turnIdx++
		}
		hitTurnIdx := turnIdx
		if hitTurnIdx <= 0 {
			hitTurnIdx = 1
		}
		content := strings.TrimSpace(m.Content)
		if content == "" {
			flushPendingUser()
			prev = searchableMessage{}
			continue
		}
		latestMetaHit = recentAnchorHitForMessage(sid, m.Role, content, prev, hitTurnIdx, messageIdx, mtime)
		score, snippetContent, matchedTerms, contextIncluded := scoreSearchableMessage(searchableMessage{
			role:    m.Role,
			content: content,
		}, prev, terms)
		prev = searchableMessage{role: m.Role, content: content}
		if score <= 0 {
			if pendingUser != nil && m.Role == "assistant" {
				pendingUser.hit = pendingUser.withNextAssistant(content, terms)
				flushPendingUser()
			}
			continue
		}
		hit := Hit{
			SessionID:       sid,
			TurnIdx:         hitTurnIdx,
			MessageIdx:      messageIdx,
			Role:            m.Role,
			Snippet:         SnippetAround(snippetContent, terms),
			Score:           score,
			MatchedTerms:    append([]string(nil), matchedTerms...),
			ContextIncluded: contextIncluded,
			ModTime:         mtime,
		}
		if m.Role == "user" {
			pendingUser = &pendingUserHit{hit: hit, content: content}
			continue
		}
		if pendingUser != nil && contextIncluded {
			pendingUser = nil
		}
		if pendingUser != nil && !contextIncluded {
			pendingUser.hit = pendingUser.withNextAssistant(content, terms)
			flushPendingUser()
		}
		fileHits = appendBoundedHits(fileHits, hit, maxPerSession)
	}
	if pendingUser != nil {
		fileHits = appendBoundedHits(fileHits, pendingUser.hit, maxPerSession)
	}
	if len(fileHits) == 0 {
		if idScore, matchedTerms := scoreContentDetails(sid, terms); idScore > 0 && latestMetaHit.Snippet != "" {
			latestMetaHit.Score = idScore
			latestMetaHit.MatchedTerms = append([]string(nil), matchedTerms...)
			fileHits = appendBoundedHits(fileHits, latestMetaHit, maxPerSession)
		}
	}
	sortHits(fileHits)
	if maxPerSession > 0 && len(fileHits) > maxPerSession {
		fileHits = fileHits[:maxPerSession]
	}
	return fileHits, nil
}

func scorePlanFile(ctx context.Context, path, sid string, terms []string, mtime string) (Hit, bool, error) {
	if err := ctx.Err(); err != nil {
		return Hit{}, false, err
	}
	summary, found := planstate.SummarizeFile(path)
	if !found || summary.Error || summary.Label == planstate.LabelMissing || summary.Label == planstate.LabelEmpty || summary.Label == planstate.LabelError {
		return Hit{}, false, nil
	}
	content := planSearchContent(summary)
	score, matchedTerms := scoreContentDetails(content, terms)
	if score <= 0 {
		return Hit{}, false, nil
	}
	return Hit{
		SessionID:    sid,
		Role:         "plan",
		Snippet:      SnippetAround(content, terms),
		Score:        score,
		MatchedTerms: append([]string(nil), matchedTerms...),
		ModTime:      mtime,
	}, true, nil
}

func scoreLoopFile(ctx context.Context, path, sid string, terms []string, mtime string) (Hit, bool, error) {
	if err := ctx.Err(); err != nil {
		return Hit{}, false, err
	}
	content, ok, err := loopSearchContent(path, sid)
	if err != nil || !ok {
		return Hit{}, false, err
	}
	score, matchedTerms := scoreContentDetails(content, terms)
	if score <= 0 {
		return Hit{}, false, nil
	}
	return Hit{
		SessionID:    sid,
		Role:         "loop",
		Snippet:      SnippetAround(content, terms),
		Score:        score,
		MatchedTerms: append([]string(nil), matchedTerms...),
		ModTime:      mtime,
	}, true, nil
}

func scoreEventsFile(ctx context.Context, path, sid string, terms []string, maxPerSession int, mtime string) ([]Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := regularFileInfo(path); !ok {
		return nil, errors.New("events log must be a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 64*1024)
	var hits []Hit
	eventIdx := 0
	for {
		line, overLimit, err := jsonl.ReadBoundedLine(reader, maxSessionLogLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		eventIdx++
		if overLimit {
			continue
		}
		var ev eventRecord
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		preview := recoveryPreviewFromEvent(ev)
		if preview == "" {
			continue
		}
		score, matchedTerms := scoreContentDetails(preview, terms)
		if score <= 0 {
			continue
		}
		hits = appendBoundedHits(hits, Hit{
			SessionID:    sid,
			MessageIdx:   eventIdx,
			Role:         "event",
			Snippet:      SnippetAround(preview, terms),
			Score:        score,
			MatchedTerms: append([]string(nil), matchedTerms...),
			ModTime:      mtime,
		}, maxPerSession)
	}
	sortHits(hits)
	if maxPerSession > 0 && len(hits) > maxPerSession {
		hits = hits[:maxPerSession]
	}
	return hits, nil
}

func planSearchContent(summary planstate.Summary) string {
	var b strings.Builder
	if summary.Label != "" {
		b.WriteString("plan_status: ")
		b.WriteString(summary.Label)
	}
	if summary.CurrentStepIndex > 0 || summary.CurrentStep != "" || summary.CurrentStepStatus != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "current_step: %d", summary.CurrentStepIndex)
		if summary.CurrentStepStatus != "" {
			fmt.Fprintf(&b, " [%s]", summary.CurrentStepStatus)
		}
		if summary.CurrentStep != "" {
			b.WriteByte(' ')
			b.WriteString(summary.CurrentStep)
		}
	}
	if summary.LastCompletedStepIndex > 0 || summary.LastCompletedStep != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "last_completed_step: %d", summary.LastCompletedStepIndex)
		if summary.LastCompletedStep != "" {
			b.WriteByte(' ')
			b.WriteString(summary.LastCompletedStep)
		}
	}
	if summary.BlockedStepIndex > 0 || summary.BlockedStep != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "blocked_step: %d", summary.BlockedStepIndex)
		if summary.BlockedStep != "" {
			b.WriteByte(' ')
			b.WriteString(summary.BlockedStep)
		}
	}
	return b.String()
}

func recentLoopPreview(path, sid string) string {
	content, ok, err := loopSearchContent(path, sid)
	if err != nil || !ok {
		return ""
	}
	return recentSessionPreview(content)
}

func recentRecoveryPreview(path string) string {
	if _, ok := regularFileInfo(path); !ok {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 64*1024)
	latest := ""
	for {
		line, overLimit, err := jsonl.ReadBoundedLine(reader, maxSessionLogLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return latest
		}
		if overLimit {
			continue
		}
		line = []byte(strings.TrimRight(string(line), "\r\n"))
		var ev eventRecord
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if preview := recoveryPreviewFromEvent(ev); preview != "" {
			latest = recentSessionPreview(preview)
		}
	}
	return latest
}

type eventRecord struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func recoveryPreviewFromEvent(ev eventRecord) string {
	switch ev.Type {
	case "conversation.repaired":
		var p struct {
			MissingToolResults    int    `json:"missing_tool_results,omitempty"`
			DuplicateToolResults  int    `json:"duplicate_tool_results,omitempty"`
			UnexpectedToolResults int    `json:"unexpected_tool_results,omitempty"`
			FailureKind           string `json:"failure_kind,omitempty"`
			Next                  string `json:"next,omitempty"`
		}
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return ""
		}
		parts := []string{"conversation_repaired"}
		if p.FailureKind != "" {
			parts = append(parts, "kind="+p.FailureKind)
		}
		if p.MissingToolResults > 0 {
			parts = append(parts, fmt.Sprintf("missing_tool_results=%d", p.MissingToolResults))
		}
		if p.DuplicateToolResults > 0 {
			parts = append(parts, fmt.Sprintf("duplicate_tool_results=%d", p.DuplicateToolResults))
		}
		if p.UnexpectedToolResults > 0 {
			parts = append(parts, fmt.Sprintf("unexpected_tool_results=%d", p.UnexpectedToolResults))
		}
		if strings.TrimSpace(p.Next) != "" {
			parts = append(parts, "next="+strings.TrimSpace(p.Next))
		}
		return strings.Join(parts, "; ")
	case "error":
		var p struct {
			Code        string `json:"code"`
			Message     string `json:"message"`
			FailureKind string `json:"failure_kind,omitempty"`
			Recoverable bool   `json:"recoverable"`
		}
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return ""
		}
		parts := []string{"runtime_error"}
		if p.Code != "" {
			parts = append(parts, "code="+p.Code)
		}
		if p.FailureKind != "" {
			parts = append(parts, "kind="+p.FailureKind)
		}
		if p.Recoverable {
			parts = append(parts, "recoverable=true")
		}
		if strings.TrimSpace(p.Message) != "" {
			parts = append(parts, "message="+strings.TrimSpace(p.Message))
		}
		return strings.Join(parts, "; ")
	case "turn.end":
		var p struct {
			Reason    string `json:"reason"`
			ToolStats *struct {
				ToolFailureByKind      map[string]int `json:"tool_failure_by_kind,omitempty"`
				LoopGuardInterventions int            `json:"loop_guard_interventions,omitempty"`
				ToolContextTruncated   int            `json:"tool_context_truncated,omitempty"`
			} `json:"tool_stats,omitempty"`
		}
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return ""
		}
		if p.Reason != "max_turns" && p.Reason != "error" {
			return ""
		}
		parts := []string{"turn_end: reason=" + p.Reason}
		if p.ToolStats != nil {
			if kind, count := topFailureKind(p.ToolStats.ToolFailureByKind); kind != "" {
				parts = append(parts, fmt.Sprintf("top_failure=%s:%d", kind, count))
			}
			if p.ToolStats.LoopGuardInterventions > 0 {
				parts = append(parts, fmt.Sprintf("loop_guards=%d", p.ToolStats.LoopGuardInterventions))
			}
			if p.ToolStats.ToolContextTruncated > 0 {
				parts = append(parts, fmt.Sprintf("tool_context_truncated=%d", p.ToolStats.ToolContextTruncated))
			}
		}
		return strings.Join(parts, "; ")
	case "loop.decision":
		var p struct {
			Kind           string `json:"kind"`
			Decision       string `json:"decision"`
			RequiredAction string `json:"required_action,omitempty"`
			VisibleInUI    *bool  `json:"visible_in_ui,omitempty"`
		}
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return ""
		}
		if p.VisibleInUI != nil && !*p.VisibleInUI {
			return ""
		}
		if strings.TrimSpace(p.RequiredAction) == "" {
			return ""
		}
		return "loop_decision: kind=" + strings.TrimSpace(p.Kind) + "; decision=" + strings.TrimSpace(p.Decision) + "; action=" + strings.TrimSpace(p.RequiredAction)
	case "context.compacted":
		var p contextCompactRecord
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return ""
		}
		var raw struct {
			SummaryPresent *bool `json:"summary_present"`
		}
		_ = json.Unmarshal(ev.Data, &raw)
		state := contextSummaryState(p, raw.SummaryPresent != nil)
		if state != "missing" && state != "empty" {
			return ""
		}
		parts := []string{"context_compaction: summary=" + state}
		if p.RemovedMessages > 0 {
			parts = append(parts, fmt.Sprintf("removed_messages=%d", p.RemovedMessages))
		}
		if strings.TrimSpace(p.Reason) != "" {
			parts = append(parts, "reason="+strings.TrimSpace(p.Reason))
		}
		if p.Reactive {
			parts = append(parts, "reactive=true")
		}
		return strings.Join(parts, "; ")
	default:
		return ""
	}
}

func topFailureKind(counts map[string]int) (string, int) {
	var top string
	var topCount int
	for kind, count := range counts {
		kind = strings.TrimSpace(kind)
		if kind == "" || count <= 0 {
			continue
		}
		if count > topCount || (count == topCount && (top == "" || kind < top)) {
			top = kind
			topCount = count
		}
	}
	return top, topCount
}

type contextCompactRecord struct {
	RemovedMessages int    `json:"removed_messages"`
	Reactive        bool   `json:"reactive"`
	Reason          string `json:"reason"`
	SummaryPresent  bool   `json:"summary_present"`
	SummaryBytes    int    `json:"summary_bytes,omitempty"`
	SummaryPreview  string `json:"summary_preview,omitempty"`
}

func contextSummaryState(p contextCompactRecord, summaryPresentKnown bool) string {
	if !summaryPresentKnown {
		return "unknown"
	}
	if !p.SummaryPresent {
		return "missing"
	}
	if p.SummaryBytes <= 0 && strings.TrimSpace(p.SummaryPreview) == "" {
		return "empty"
	}
	return "present"
}

func loopSearchContent(path, sid string) (string, bool, error) {
	summary, found, err := loopstate.SummarizeFile(path, loopstate.ProtocolRelPath(sid))
	if err != nil || !found {
		return "", false, err
	}
	protocol, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		return "", false, err
	}
	var b strings.Builder
	if summary.Status != "" {
		fmt.Fprintf(&b, "loop_status: %s\n", summary.Status)
	}
	if summary.LoopID != "" {
		fmt.Fprintf(&b, "loop_id: %s\n", summary.LoopID)
	}
	if summary.OwnerSession != "" {
		fmt.Fprintf(&b, "owner_session: %s\n", summary.OwnerSession)
	}
	appendLoopStateSearchContent(&b, summary.State)
	appendLoopEventSearchContent(&b, filepath.Join(filepath.Dir(path), loopstate.EventsFileName))
	if current := markdownSection(protocol, "## 2. Current Situation"); current != "" {
		b.WriteString("current_situation:\n")
		b.WriteString(current)
		b.WriteByte('\n')
	}
	if northStar := markdownSection(protocol, "## 1. North Star"); northStar != "" {
		b.WriteString("north_star:\n")
		b.WriteString(northStar)
		b.WriteByte('\n')
	}
	b.WriteString("protocol:\n")
	b.WriteString(protocol)
	return b.String(), true, nil
}

func appendLoopEventSearchContent(b *strings.Builder, path string) {
	if b == nil || strings.TrimSpace(path) == "" {
		return
	}
	events, found, err := loopstate.ReadRecentEvents(path, 3)
	if err != nil || !found || len(events) == 0 {
		return
	}
	b.WriteString("recent_loop_events:")
	for _, ev := range events {
		b.WriteString("\n- ")
		appendLoopStateLine(b, "event", loopEventSearchFields(ev)...)
	}
	b.WriteByte('\n')
}

func loopEventSearchFields(ev loopstate.Event) []string {
	return []string{
		"type=" + stateSearchValue(ev.Type),
		"mode=" + stateSearchValue(ev.Mode),
		"feed=" + stateSearchInt(ev.FeedNumber),
		"plan_label=" + stateSearchValue(ev.PlanLabel),
		"plan_step_index=" + stateSearchInt(ev.PlanStepIndex),
		"plan_step_status=" + stateSearchValue(ev.PlanStepStatus),
		"plan_step=" + stateSearchValue(ev.PlanStep),
		"turn_end=" + stateSearchValue(ev.TurnEndReason),
		"tools=" + stateSearchInt(ev.ToolRequests),
		"tool_errors=" + stateSearchInt(ev.ToolErrors),
		"loop_guards=" + stateSearchInt(ev.LoopGuards),
		"forced_no_tools=" + stateSearchInt(ev.ForcedNoTools),
		"memory_updates=" + stateSearchInt(ev.MemoryUpdates),
		"memory_searches=" + stateSearchInt(ev.MemorySearches),
		"memory_misses=" + stateSearchInt(ev.MemoryMisses),
		"session_search=" + stateSearchInt(ev.SessionSearch),
		"decision=" + stateSearchValue(ev.Decision),
		"action=" + stateSearchValue(ev.RequiredAction),
		"calibration=" + stateSearchValue(ev.Calibration),
		"summary=" + stateSearchValue(ev.Summary),
		"reason=" + stateSearchValue(ev.Reason),
	}
}

func appendLoopStateSearchContent(b *strings.Builder, state *loopstate.State) {
	if b == nil || state == nil {
		return
	}
	appendLoopStateLine(b, "loop_state",
		"status="+stateSearchValue(state.Status),
		"updates="+stateSearchInt(state.ProtocolUpdates),
		"feeds="+stateSearchInt(state.ProtocolFeeds),
		"calibration_answers="+stateSearchInt(state.CalibrationAnswers),
	)
	appendLoopStateLine(b, "last_event",
		"type="+stateSearchValue(state.LastEventType),
		"summary="+stateSearchValue(state.LastEventSummary),
	)
	appendLoopStateLine(b, "last_plan",
		"label="+stateSearchValue(state.LastPlanLabel),
		"step_index="+stateSearchInt(state.LastPlanStepIndex),
		"step_status="+stateSearchValue(state.LastPlanStepStatus),
		"step="+stateSearchValue(state.LastPlanStep),
	)
	appendLoopStateLine(b, "last_turn",
		"id="+stateSearchValue(state.LastTurnID),
		"reason="+stateSearchValue(state.LastTurnEndReason),
		"tools="+stateSearchInt(state.LastTurnToolRequests),
		"tool_errors="+stateSearchInt(state.LastTurnToolErrors),
		"loop_guards="+stateSearchInt(state.LastTurnLoopGuards),
		"memory_updates="+stateSearchInt(state.LastTurnMemoryUpdates),
		"memory_searches="+stateSearchInt(state.LastTurnMemorySearches),
		"memory_misses="+stateSearchInt(state.LastTurnMemoryMisses),
		"session_search="+stateSearchInt(state.LastTurnSessionSearch),
	)
	appendLoopStateLine(b, "last_memory_update",
		"action="+stateSearchValue(state.LastMemoryUpdateAction),
		"target="+stateSearchValue(state.LastMemoryUpdateTarget),
		"topic="+stateSearchValue(state.LastMemoryUpdateTopic),
		"location="+stateSearchValue(state.LastMemoryUpdateLoc),
		"preview="+stateSearchValue(state.LastMemoryUpdate),
	)
	appendLoopStateLine(b, "last_decision",
		"kind="+stateSearchValue(state.LastDecisionKind),
		"trigger="+stateSearchValue(state.LastDecisionTrigger),
		"decision="+stateSearchValue(state.LastDecision),
		"confidence="+stateSearchValue(state.LastDecisionConfidence),
		"action="+stateSearchValue(state.LastDecisionAction),
	)
	appendLoopStateLine(b, "last_compaction",
		"reason="+stateSearchValue(state.LastCompactionReason),
		"reactive="+stateSearchBool(state.LastCompactionReactive),
		"count="+stateSearchInt(state.ContextCompactions),
	)
	if state.LastCalibrationAnswer != "" {
		appendLoopStateLine(b, "last_calibration", "answer="+stateSearchValue(state.LastCalibrationAnswer))
	}
}

func appendLoopStateLine(b *strings.Builder, label string, fields ...string) {
	var parts []string
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(key)+"="+strings.TrimSpace(value))
	}
	if len(parts) == 0 {
		return
	}
	fmt.Fprintf(b, "%s: %s\n", label, strings.Join(parts, " "))
}

func stateSearchValue(value string) string {
	value = textutil.StripASCIIControls(value)
	value = textutil.CompactWhitespace(value)
	return textutil.Preview(value, 300)
}

func stateSearchInt(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

func stateSearchBool(v bool) string {
	if !v {
		return ""
	}
	return "true"
}

func markdownSection(markdown, heading string) string {
	heading = strings.TrimSpace(heading)
	if heading == "" {
		return ""
	}
	var lines []string
	inSection := false
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if inSection {
				break
			}
			if trimmed == heading {
				inSection = true
			}
			continue
		}
		if inSection {
			lines = append(lines, line)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func recentAnchorHitForMessage(sid, role, content string, prev searchableMessage, turnIdx, messageIdx int, mtime string) Hit {
	snippet := content
	contextIncluded := false
	if role == "assistant" && prev.role == "user" && strings.TrimSpace(prev.content) != "" {
		snippet = "user: " + prev.content + "\nassistant: " + content
		contextIncluded = true
	}
	return Hit{
		SessionID:       sid,
		TurnIdx:         turnIdx,
		MessageIdx:      messageIdx,
		Role:            role,
		Snippet:         TruncateSnippet(snippet, snippetLen),
		ContextIncluded: contextIncluded,
		ModTime:         mtime,
	}
}

type pendingUserHit struct {
	hit     Hit
	content string
}

func (p pendingUserHit) withNextAssistant(assistantContent string, terms []string) Hit {
	combined := "user: " + p.content + "\nassistant: " + assistantContent
	score, matchedTerms := scoreContentDetails(combined, terms)
	hit := p.hit
	if score > 0 {
		hit.Score = score
		hit.MatchedTerms = append([]string(nil), matchedTerms...)
	}
	hit.Snippet = SnippetAround(combined, terms)
	hit.ContextIncluded = true
	return hit
}

type searchableMessage struct {
	role    string
	content string
}

func scoreSearchableMessage(cur, prev searchableMessage, terms []string) (float64, string, []string, bool) {
	score, matchedTerms := scoreContentDetails(cur.content, terms)
	if score <= 0 {
		return 0, cur.content, nil, false
	}
	if cur.role != "assistant" || prev.role != "user" || strings.TrimSpace(prev.content) == "" {
		return score, cur.content, matchedTerms, false
	}
	combined := prev.content + "\n" + cur.content
	combinedScore, combinedTerms := scoreContentDetails(combined, terms)
	if combinedScore <= score {
		return score, cur.content, matchedTerms, false
	}
	return combinedScore, "user: " + prev.content + "\nassistant: " + cur.content, combinedTerms, true
}

func regularFileModTime(path string) (string, bool) {
	info, ok := regularFileInfo(path)
	if !ok {
		return "", false
	}
	return info.ModTime().UTC().Format(time.RFC3339), true
}

func newestRegularFileModTime(paths ...string) (string, bool) {
	var newest time.Time
	found := false
	for _, path := range paths {
		info, ok := regularFileInfo(path)
		if !ok {
			continue
		}
		if !found || info.ModTime().After(newest) {
			newest = info.ModTime()
			found = true
		}
	}
	if !found {
		return "", false
	}
	return newest.UTC().Format(time.RFC3339), true
}

func regularFileInfo(path string) (os.FileInfo, bool) {
	info, err := os.Lstat(path)
	if err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, false
	}
	return info, true
}

func entryIsSymlink(ent os.DirEntry) bool {
	info, err := ent.Info()
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func appendBoundedHits(hits []Hit, hit Hit, limit int) []Hit {
	if limit <= 0 {
		return append(hits, hit)
	}
	hits = append(hits, hit)
	sortHits(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func sortHits(hits []Hit) {
	sort.SliceStable(hits, func(i, j int) bool { return hitLess(hits[i], hits[j]) })
}

func hitLess(a, b Hit) bool {
	if a.Score != b.Score {
		if scoresCloseForRecency(a.Score, b.Score) && a.ModTime != b.ModTime {
			return a.ModTime > b.ModTime
		}
		return a.Score > b.Score
	}
	if a.ModTime != b.ModTime {
		return a.ModTime > b.ModTime
	}
	if a.SessionID != b.SessionID {
		return a.SessionID < b.SessionID
	}
	return a.TurnIdx < b.TurnIdx
}

func scoresCloseForRecency(a, b float64) bool {
	high := a
	if b > high {
		high = b
	}
	if high <= 0 {
		return false
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= high*0.15
}

// Tokenize lowercases and splits on non-letter / non-digit runes across
// scripts. CJK letters are emitted as individual rune tokens because those
// languages often have no spaces; other letters/digits keep whole-token
// matching. Tokens shorter than 2 bytes and common English stopwords are
// dropped.
func Tokenize(s string) []string {
	s = NormalizeQuery(s)
	s = strings.ToLower(s)
	var raw []string
	var cur strings.Builder
	flush := func() {
		t := cur.String()
		cur.Reset()
		if len(t) >= 2 {
			raw = append(raw, t)
		}
	}
	for _, r := range s {
		if isCJKLetter(r) {
			flush()
			raw = append(raw, string(r))
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	out := raw[:0]
	seen := map[string]bool{}
	for _, t := range raw {
		if stopwords[t] || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) >= MaxQueryTerms {
			break
		}
	}
	return out
}

func isCJKLetter(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

func NormalizeQuery(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= MaxQueryBytes {
		return s
	}
	cut := textutil.AlignBackward(s, MaxQueryBytes)
	return strings.TrimSpace(s[:cut])
}

var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true,
	"at": true, "be": true, "but": true, "by": true, "for": true,
	"from": true, "has": true, "have": true, "in": true, "is": true,
	"it": true, "its": true, "no": true, "not": true, "of": true,
	"on": true, "or": true, "that": true, "the": true, "this": true,
	"to": true, "was": true, "we": true, "were": true, "what": true,
	"when": true, "where": true, "which": true, "who": true, "will": true,
	"with": true, "you": true, "your": true,
}

// ScoreContent counts unique-term overlap with a small boost for
// total-occurrence count.
func ScoreContent(content string, terms []string) float64 {
	score, _ := scoreContentDetails(content, terms)
	return score
}

func scoreContentDetails(content string, terms []string) (float64, []string) {
	counts := countContentTerms(content, terms)
	unique, total := 0, 0
	for _, c := range counts {
		unique++
		total += c
	}
	if unique == 0 {
		return 0, nil
	}
	return float64(unique) + 0.1*float64(total), matchedTermsInQueryOrder(terms, counts)
}

func matchedTermsInQueryOrder(terms []string, counts map[string]int) []string {
	if len(counts) == 0 {
		return nil
	}
	out := make([]string, 0, len(counts))
	seen := map[string]bool{}
	for _, term := range terms {
		if term == "" || seen[term] || counts[term] <= 0 {
			continue
		}
		seen[term] = true
		out = append(out, term)
	}
	return out
}

func countContentTerms(content string, terms []string) map[string]int {
	want := make(map[string]bool, len(terms))
	for _, term := range terms {
		if term != "" {
			want[term] = true
		}
	}
	counts := map[string]int{}
	var cur strings.Builder
	flush := func() {
		t := strings.ToLower(cur.String())
		cur.Reset()
		if want[t] {
			counts[t]++
		}
	}
	for _, r := range content {
		if isCJKLetter(r) {
			flush()
			t := strings.ToLower(string(r))
			if want[t] {
				counts[t]++
			}
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return counts
}

// snippetLen is intentionally larger than a search-engine teaser. Session
// search is used by the agent as working evidence; small models often need the
// surrounding conclusion/test result in the same hit instead of a clipped
// fragment that forces another query.
const snippetLen = 900

// SnippetAround returns a UTF-8-safe substring of content centered on
// the first term hit.
func SnippetAround(content string, terms []string) string {
	hitIdx := firstTermTokenIndex(content, terms)
	if hitIdx < 0 {
		return TruncateSnippet(content, snippetLen)
	}
	half := snippetLen / 2
	start := hitIdx - half
	if start < 0 {
		start = 0
	}
	end := start + snippetLen
	if end > len(content) {
		end = len(content)
	}
	start = textutil.AlignForward(content, start)
	end = textutil.AlignBackward(content, end)
	if start >= end {
		return ""
	}
	prefix := ""
	if start > 0 {
		prefix = "..."
	}
	suffix := ""
	if end < len(content) {
		suffix = "..."
	}
	return prefix + content[start:end] + suffix
}

type termHit struct {
	start int
	term  string
}

func firstTermTokenIndex(content string, terms []string) int {
	want := make(map[string]bool, len(terms))
	for _, term := range terms {
		if term != "" {
			want[term] = true
		}
	}
	bestStart := -1
	bestCovered := 0
	var window []termHit
	counts := map[string]int{}
	half := snippetLen / 2
	consider := func(hit termHit) {
		window = append(window, hit)
		counts[hit.term]++
		minStart := hit.start - half
		trim := 0
		for trim < len(window) && window[trim].start < minStart {
			counts[window[trim].term]--
			if counts[window[trim].term] == 0 {
				delete(counts, window[trim].term)
			}
			trim++
		}
		if trim > 0 {
			window = window[trim:]
		}
		if len(counts) > bestCovered {
			bestStart = hit.start
			bestCovered = len(counts)
		}
	}
	tokenStart := -1
	var cur strings.Builder
	flush := func() {
		t := strings.ToLower(cur.String())
		cur.Reset()
		if tokenStart >= 0 && want[t] {
			consider(termHit{start: tokenStart, term: t})
		}
		tokenStart = -1
	}
	for i, r := range content {
		if isCJKLetter(r) {
			flush()
			t := strings.ToLower(string(r))
			if want[t] {
				consider(termHit{start: i, term: t})
			}
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if tokenStart < 0 {
				tokenStart = i
			}
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return bestStart
}

func TruncateSnippet(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := textutil.AlignBackward(s, n)
	return s[:cut] + "..."
}
