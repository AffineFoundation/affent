// affentctl is a standalone CLI driver for the affent loop. Two modes:
//
//	affentctl run   — one-shot: feed a prompt, get a final answer + JSONL
//	                  trace. Meant for training / eval pipelines.
//	affentctl chat  — REPL: ongoing dialogue with the same conversation
//	                  state, like codex/goose. Best for interactive
//	                  debugging.
//	affentctl sessions — list previous sessions stored under workspace.
//
// Both run and chat support --session-id <id> and --continue to pick up
// an existing conversation log instead of starting fresh.
//
// No HTTP server, no Postgres, no Docker. Just one affent loop with the
// built-in shell + file tools, talking to an OpenAI-compatible model.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := loadDotEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "affentctl: load .env: %v\n", err)
		os.Exit(64)
	}
	if len(os.Args) < 2 {
		usage()
		os.Exit(64)
	}
	switch os.Args[1] {
	case "run":
		os.Exit(runCmd(os.Args[2:]))
	case "chat":
		os.Exit(chatCmd(os.Args[2:]))
	case "sessions", "ls":
		os.Exit(sessionsCmd(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", os.Args[1])
		usage()
		os.Exit(64)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: affentctl <command> [flags]

Commands:
  run        one-shot: --prompt → final answer + JSONL trace
  chat       REPL: multi-turn dialogue
  sessions   list previous sessions in --workspace
  help       show this message

Run 'affentctl <command> -h' for command-specific flags.`)
}
