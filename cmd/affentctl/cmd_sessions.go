package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// sessionsCmd lists prior sessions found under <workspace>/.affentctl/.
// For each session shows the id, mtime, message count, and the first
// user message as a preview. Useful before deciding what to --continue.
func sessionsCmd(args []string) int {
	fs := flag.NewFlagSet("sessions", flag.ExitOnError)
	workspace := fs.String("workspace", "./affent-workspace", "working dir to inspect")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `usage: affentctl sessions [--workspace DIR]

List prior conversation logs under <workspace>/.affentctl/, newest
first. Each row: <session_id>  <mtime>  <messages>  <first user msg>.`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	convDir := filepath.Join(*workspace, ".affentctl")
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
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
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
		fmt.Printf("%s\t%s\t%d msgs\t%s\n",
			r.sid,
			r.mt.Local().Format("2006-01-02 15:04"),
			r.nMsgs,
			r.preview)
	}
	return 0
}

// scanLog walks the JSONL conversation log and returns (message count,
// first user message preview). Cheap pass; we don't load everything
// into memory.
func scanLog(path string) (int, string) {
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

func oneLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) > max {
		return trimUTF8(s, max-1) + "…"
	}
	return s
}
