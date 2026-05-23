package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/affinefoundation/affent/internal/planstate"
)

const maxLocalSessionPlanBytes = planstate.MaxFileBytes

// sessionsCmd lists prior sessions found under <workspace>/.affentctl/.
// For each session shows the id, mtime, message count, and the first
// user message as a preview. Useful before deciding what to --continue.
func sessionsCmd(args []string) int {
	fs := flag.NewFlagSet("sessions", flag.ExitOnError)
	workspace := fs.String("workspace", "./affent-workspace", "working dir to inspect")
	planID := fs.String("plan", "", "print the persisted plan JSON for a session id")
	clearPlanID := fs.String("clear-plan", "", "remove the persisted plan JSON for a session id")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `usage: affentctl sessions [--workspace DIR] [--plan SESSION_ID] [--clear-plan SESSION_ID]

List prior conversation logs under <workspace>/.affentctl/, newest
first. Each row: <session_id>  <mtime>  <messages>  <plan progress>  <first user msg>.

Use --plan SESSION_ID to print <workspace>/.affentctl/<session_id>.plan.json
without starting or resuming the agent.

Use --clear-plan SESSION_ID to remove that session's persisted plan without
starting or resuming the agent.`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument(s): %s\n", strings.Join(fs.Args(), " "))
		return exitUsage
	}

	convDir := filepath.Join(*workspace, ".affentctl")
	if *planID != "" && *clearPlanID != "" {
		fmt.Fprintln(os.Stderr, "--plan and --clear-plan cannot be used together")
		return exitUsage
	}
	if *planID != "" {
		return printSessionPlan(convDir, *planID)
	}
	if *clearPlanID != "" {
		return clearSessionPlanCmd(convDir, *clearPlanID)
	}
	entries, err := os.ReadDir(convDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "(no sessions in this workspace)")
			return 0
		}
		fmt.Fprintln(os.Stderr, err)
		return exitRuntime
	}

	type row struct {
		sid     string
		mt      time.Time
		nMsgs   int
		preview string
	}
	var rows []row
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") || dirEntryIsSymlink(e) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		full := filepath.Join(convDir, e.Name())
		n, preview := scanLog(full)
		rows = append(rows, row{
			sid:     strings.TrimSuffix(e.Name(), ".jsonl"),
			mt:      info.ModTime(),
			nMsgs:   n,
			preview: preview,
		})
	}
	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "(no sessions in this workspace)")
		return 0
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].mt.After(rows[j].mt) })

	for _, r := range rows {
		plan := sessionPlanSummary(convDir, r.sid)
		fmt.Printf("%s\t%s\t%d msgs\t%s\t%s\n",
			r.sid,
			r.mt.Local().Format("2006-01-02 15:04"),
			r.nMsgs,
			plan,
			r.preview)
	}
	return 0
}

func clearSessionPlanCmd(convDir, sessionID string) int {
	removed, err := clearLocalSessionPlan(convDir, sessionID)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitRuntime
	}
	if !removed {
		fmt.Fprintf(os.Stderr, "no plan for session %q\n", sessionID)
		return 0
	}
	fmt.Fprintf(os.Stdout, "cleared plan for session %q\n", sessionID)
	return 0
}

func printSessionPlan(convDir, sessionID string) int {
	plan, found, err := readLocalSessionPlan(convDir, sessionID)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitRuntime
	}
	if !found {
		fmt.Fprintf(os.Stderr, "no plan for session %q\n", sessionID)
		return exitUsage
	}
	fmt.Println(string(plan))
	return 0
}

func sessionPlanExists(convDir, sessionID string) bool {
	info, err := os.Lstat(localSessionPlanPath(convDir, sessionID))
	return err == nil && !info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func sessionPlanSummary(convDir, sessionID string) string {
	return localSessionPlanSummary(convDir, sessionID).Label
}

func localSessionPlanSummary(convDir, sessionID string) planstate.Summary {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) || sessionID == "." || sessionID == ".." {
		return planstate.ErrorSummary()
	}
	summary, _ := planstate.SummarizeFile(localSessionPlanPath(convDir, sessionID))
	return summary
}

func readLocalSessionPlan(convDir, sessionID string) (json.RawMessage, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, false, errors.New("session id is required")
	}
	if strings.ContainsAny(sessionID, `/\`) || sessionID == "." || sessionID == ".." {
		return nil, false, fmt.Errorf("invalid session id %q", sessionID)
	}
	return planstate.ReadFile(localSessionPlanPath(convDir, sessionID))
}

func clearLocalSessionPlan(convDir, sessionID string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false, errors.New("session id is required")
	}
	if strings.ContainsAny(sessionID, `/\`) || sessionID == "." || sessionID == ".." {
		return false, fmt.Errorf("invalid session id %q", sessionID)
	}
	return planstate.RemoveFile(localSessionPlanPath(convDir, sessionID))
}

func localSessionPlanPath(convDir, sessionID string) string {
	return filepath.Join(convDir, sessionID+".plan.json")
}

// scanLog walks the JSONL conversation log and returns (message count,
// first user message preview). Cheap pass; we don't load everything
// into memory.
func scanLog(path string) (int, string) {
	info, err := os.Lstat(path)
	if err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0, ""
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var preview string
	count := 0
	for sc.Scan() {
		count++
		if preview != "" {
			continue
		}
		var m struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		if m.Role == "user" {
			preview = oneLine(m.Content, 60)
		}
	}
	return count, preview
}

func dirEntryIsSymlink(e os.DirEntry) bool {
	info, err := e.Info()
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func oneLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) > max {
		return trimUTF8(s, max-1) + "…"
	}
	return s
}
