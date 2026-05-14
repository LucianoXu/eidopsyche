// Package main is a stub `claude` binary used by internal/agentloop tests.
//
// Reads JSONL user messages on stdin and emits stream-json events on
// stdout, mimicking `claude --input-format stream-json --output-format
// stream-json`. Behavior is selected by --mode and parameterized by
// --at-n.
//
// Modes:
//
//	normal         — each stdin line → system init (once) + assistant + result
//	dream-then-exit — normal until stdin EOF, then exit 0
//	crash-after    — exit 1 after --at-n turns
//	hang-after     — stop emitting after --at-n turns; keep reading stdin
//	mailbox-burst  — emit a spontaneous assistant+result pair between
//	                 turn --at-n and turn --at-n+1 (no preceding stdin)
//	auth-required  — print the known auth-required marker to stderr and
//	                 exit 47 immediately (no stdin reading)
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	var mode string
	var atN int
	var sessionID string
	flag.StringVar(&mode, "mode", "normal", "stub behavior mode")
	flag.IntVar(&atN, "at-n", 0, "turn-count threshold for modes parameterized by N")
	flag.StringVar(&sessionID, "session-id", "", "ignored — accepted so the stub matches real claude's CLI surface")

	// Accept and ignore the real claude flags we pass in production so
	// argv parsing succeeds. String-valued flags are declared even
	// though we discard the values.
	for _, name := range []string{
		"input-format", "output-format", "system-prompt",
		"model", "effort", "resume", "p", "tools", "mcp-config",
	} {
		flag.String(name, "", "(accepted but unused by stub; mirrors claude CLI)")
	}
	flag.Bool("verbose", false, "(accepted)")
	flag.Bool("include-partial-messages", false, "(accepted)")
	flag.Bool("dangerously-skip-permissions", false, "(accepted)")
	flag.Parse()

	if mode == "auth-required" {
		fmt.Fprintln(os.Stderr, "Invalid bearer token: please run /login to authenticate")
		os.Exit(47)
	}

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	emit := func(ev map[string]any) {
		line, _ := json.Marshal(ev)
		fmt.Fprintln(out, string(line))
		_ = out.Flush()
	}

	sysInitEmitted := false
	in := bufio.NewReader(os.Stdin)
	turn := 0
	for {
		line, err := in.ReadString('\n')
		if len(line) > 0 {
			if !sysInitEmitted {
				emit(map[string]any{
					"type":       "system",
					"subtype":    "init",
					"session_id": sessionID,
				})
				sysInitEmitted = true
			}
			emit(map[string]any{
				"type":       "assistant",
				"session_id": sessionID,
				"message":    map[string]any{"role": "assistant", "content": []any{}},
			})
			emit(map[string]any{
				"type":           "result",
				"subtype":        "success",
				"session_id":     sessionID,
				"result":         "ok",
				"total_cost_usd": 0.0,
			})
			turn++
			if mode == "crash-after" && turn >= atN {
				os.Exit(1)
			}
			if mode == "hang-after" && turn >= atN {
				_, _ = io.Copy(io.Discard, in)
				return
			}
			if mode == "mailbox-burst" && turn == atN {
				time.Sleep(10 * time.Millisecond)
				emit(map[string]any{
					"type":       "assistant",
					"session_id": sessionID,
				})
				emit(map[string]any{
					"type":       "result",
					"subtype":    "success",
					"session_id": sessionID,
				})
			}
		}
		if err == io.EOF {
			return
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "stub: read error:", err)
			os.Exit(2)
		}
	}
}
