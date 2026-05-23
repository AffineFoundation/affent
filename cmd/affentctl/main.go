// affentctl is a standalone CLI driver for the affent loop. Two modes:
//
//	affentctl run   — one-shot: feed a prompt, get a final answer + JSONL
//	                  trace. Meant for training / eval pipelines.
//	affentctl chat  — REPL: ongoing dialogue with the same conversation
//	                  state, like codex/goose. Best for interactive
//	                  debugging.
//	affentctl sessions — list previous sessions and inspect local plans.
//
// Both run and chat support --session-id <id> and --continue to pick up
// an existing conversation log instead of starting fresh.
//
// No HTTP server, no Postgres. Just one affent loop with the built-in
// shell + file tools, talking to an OpenAI-compatible model. Docker is
// optional via `affentctl sandbox start` + `--executor docker:<name>`.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := loadDotEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "affentctl: load .env: %v\n", err)
		os.Exit(exitUsage)
	}
	if len(os.Args) < 2 {
		usage()
		os.Exit(exitUsage)
	}
	switch os.Args[1] {
	case "run":
		os.Exit(runCmd(os.Args[2:]))
	case "chat":
		os.Exit(chatCmd(os.Args[2:]))
	case "doctor":
		os.Exit(doctorCmd(os.Args[2:]))
	case "sandbox":
		os.Exit(sandboxCmd(os.Args[2:]))
	case "image":
		os.Exit(imageCmd(os.Args[2:]))
	case "sessions", "ls":
		os.Exit(sessionsCmd(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", os.Args[1])
		usage()
		os.Exit(exitUsage)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: affentctl <command> [flags]

Commands:
  run        one-shot: --prompt → final answer + JSONL trace
  chat       REPL: multi-turn dialogue
  doctor     check config, workspace, executor, and Docker readiness
  sandbox    start a persistent, memory-limited Docker tool container
  image      build the full Affent runtime Docker image
  sessions   list previous sessions and inspect local plans
  help       show this message

Run 'affentctl <command> -h' for command-specific flags.`)
}
