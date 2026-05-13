// Package main implements promptdump, a dev utility that captures the
// Anthropic Messages API request body Claude Code sends, so the verbatim
// default system prompt can be inspected under any combination of claude
// CLI flags. The capture core lives in
// github.com/LucianoXu/eidopsyche/internal/promptcapture; this wrapper
// stays outside go.work as a host-only dev tool.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/promptcapture"
)

type runOpts struct {
	Prompt    string
	ExtraArgs []string
	Verbose   bool
	OutPath   string
}

func parseFlags(argv []string) (runOpts, error) {
	fs := flag.NewFlagSet(argv[0], flag.ContinueOnError)
	prompt := fs.String("p", "ping", "prompt passed to claude as -p <prompt>")
	outFlag := fs.String("o", "", "write captured envelope to this path (default: stdout JSON)")
	verbose := fs.Bool("v", false, "verbose: log proxy traffic and claude stderr")

	var ourArgs, extra []string
	sep := -1
	for i, a := range argv[1:] {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep == -1 {
		ourArgs = argv[1:]
	} else {
		ourArgs = argv[1 : 1+sep]
		extra = argv[1+sep+1:]
	}
	if err := fs.Parse(ourArgs); err != nil {
		return runOpts{}, err
	}
	return runOpts{
		Prompt: *prompt, ExtraArgs: extra, Verbose: *verbose, OutPath: *outFlag,
	}, nil
}

func main() {
	opts, err := parseFlags(os.Args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "promptdump: %v\n", err)
		os.Exit(2)
	}

	claudePath, err := exec.LookPath("claude")
	if err != nil {
		fmt.Fprintf(os.Stderr, "promptdump: claude binary not on PATH: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	env, err := promptcapture.Run(ctx, promptcapture.Opts{
		ClaudeBin: claudePath,
		Prompt:    opts.Prompt,
		ExtraArgs: opts.ExtraArgs,
		Verbose:   opts.Verbose,
		// CapturedFrom intentionally zero — this is the host shim, not
		// bound to any mind-form.
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "promptdump: %v\n", err)
		os.Exit(1)
	}

	if opts.OutPath == "" {
		js, err := json.MarshalIndent(env, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "promptdump: %v\n", err)
			os.Exit(1)
		}
		os.Stdout.Write(js)
		os.Stdout.Write([]byte("\n"))
		return
	}
	wrote, err := promptcapture.WriteOutputs(env, opts.OutPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "promptdump: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "promptdump: wrote %s\n", strings.Join(wrote, ", "))
}
