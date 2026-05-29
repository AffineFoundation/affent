package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/jsonl"
	"github.com/affinefoundation/affent/internal/sse"
)

var (
	errSessionEditBusy                 = errors.New("session has an active turn")
	errSessionEditTurnNotFound         = errors.New("edit turn not found")
	errSessionEditConversationMismatch = errors.New("edit turn is not present in conversation log")
)

type sessionMessageEditResult struct {
	RemovedEvents int `json:"removed_events"`
}

type sessionEditCut struct {
	keepLines         [][]byte
	removedEvents     int
	targetUserOrdinal int
	targetText        string
}

func validateSessionTurnID(turnID string) error {
	if turnID == "" {
		return errors.New("turn id is required")
	}
	if strings.ContainsAny(turnID, "/\\\x00") || strings.Contains(turnID, "..") {
		return fmt.Errorf("invalid turn id %q", turnID)
	}
	if strings.TrimSpace(turnID) != turnID {
		return fmt.Errorf("invalid turn id %q", turnID)
	}
	return nil
}

func truncateSessionForMessageEdit(pool *SessionPool, sessionID, turnID string) (sessionMessageEditResult, error) {
	if pool == nil {
		return sessionMessageEditResult{}, os.ErrNotExist
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()

	if sess := pool.sessions[sessionID]; sess != nil {
		if sess.isActiveTurn() {
			return sessionMessageEditResult{}, errSessionEditBusy
		}
		delete(pool.sessions, sessionID)
		if err := sess.Close(); err != nil {
			return sessionMessageEditResult{}, err
		}
	}

	dir := pool.sessionDirPath(sessionID)
	cut, err := sessionEditCutFromEvents(filepath.Join(dir, "events.jsonl"), turnID)
	if err != nil {
		return sessionMessageEditResult{}, err
	}
	if err := truncateConversationForMessageEdit(filepath.Join(dir, "conversation.jsonl"), cut.targetUserOrdinal, cut.targetText); err != nil {
		return sessionMessageEditResult{}, err
	}
	if err := rewriteJSONLLines(filepath.Join(dir, "events.jsonl"), cut.keepLines); err != nil {
		return sessionMessageEditResult{}, err
	}
	return sessionMessageEditResult{RemovedEvents: cut.removedEvents}, nil
}

func sessionEditCutFromEvents(path, turnID string) (sessionEditCut, error) {
	if err := rejectUnsafeJSONLFile(path, "events"); err != nil {
		return sessionEditCut{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return sessionEditCut{}, err
	}
	defer f.Close()

	reader := bufio.NewReaderSize(f, 64*1024)
	var lines [][]byte
	cutLine := -1
	userOrdinal := 0
	targetUserOrdinal := 0
	targetText := ""
	for {
		line, overLimit, err := jsonl.ReadBoundedLine(reader, maxHistoryLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return sessionEditCut{}, err
		}
		if overLimit {
			return sessionEditCut{}, fmt.Errorf("events log contains an oversized line")
		}
		line = ensureJSONLNewline(line)
		lineNo := len(lines)
		lines = append(lines, append([]byte(nil), line...))
		var ev sse.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		eventTurnID := eventPayloadTurnID(ev)
		if eventTurnID == turnID && cutLine < 0 {
			cutLine = lineNo
		}
		if ev.Type != sse.TypeUserMessage {
			continue
		}
		var p sse.UserMessagePayload
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			continue
		}
		userOrdinal++
		if p.TurnID == turnID {
			targetUserOrdinal = userOrdinal
			targetText = p.Text
			if cutLine < 0 {
				cutLine = lineNo
			}
		}
	}
	if cutLine < 0 || targetUserOrdinal == 0 {
		return sessionEditCut{}, errSessionEditTurnNotFound
	}
	return sessionEditCut{
		keepLines:         lines[:cutLine],
		removedEvents:     len(lines) - cutLine,
		targetUserOrdinal: targetUserOrdinal,
		targetText:        targetText,
	}, nil
}

func truncateConversationForMessageEdit(path string, targetUserOrdinal int, targetText string) error {
	if err := rejectUnsafeJSONLFile(path, "conversation"); err != nil {
		return err
	}
	conv, err := agent.OpenConversationAt(path)
	if err != nil {
		return err
	}
	msgs := conv.Snapshot()
	userOrdinal := 0
	cutIndex := -1
	for i, msg := range msgs {
		if msg.Role != "user" {
			continue
		}
		userOrdinal++
		if userOrdinal != targetUserOrdinal {
			continue
		}
		if targetText != "" && msg.Content != targetText {
			return fmt.Errorf("%w: user message %d content does not match event log", errSessionEditConversationMismatch, targetUserOrdinal)
		}
		cutIndex = i
		break
	}
	if cutIndex < 0 {
		return errSessionEditConversationMismatch
	}
	return conv.Replace(msgs[:cutIndex])
}

func eventPayloadTurnID(ev sse.Event) string {
	var p struct {
		TurnID string `json:"turn_id"`
	}
	if err := json.Unmarshal(ev.Data, &p); err != nil {
		return ""
	}
	return p.TurnID
}

func rejectUnsafeJSONLFile(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s path is a directory", label)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s path must not be a symlink", label)
	}
	return nil
}

func rewriteJSONLLines(path string, lines [][]byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	for _, line := range lines {
		if _, err := f.Write(ensureJSONLNewline(line)); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	if d, derr := os.Open(filepath.Dir(path)); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func ensureJSONLNewline(line []byte) []byte {
	if len(line) == 0 || bytes.HasSuffix(line, []byte("\n")) {
		return line
	}
	out := append([]byte(nil), line...)
	out = append(out, '\n')
	return out
}
