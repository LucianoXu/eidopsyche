// Package main is a test stub that mimics enough of `claude` for
// promptcapture tests: it POSTs a canned /v1/messages body to
// $ANTHROPIC_BASE_URL using $ANTHROPIC_API_KEY, then exits.
//
// Build at test time via `go build -o <dir>/stubclaude ./testdata/stubclaude`.
package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	base := os.Getenv("ANTHROPIC_BASE_URL")
	if base == "" {
		fmt.Fprintln(os.Stderr, "stubclaude: ANTHROPIC_BASE_URL unset")
		os.Exit(2)
	}
	body := []byte(`{"model":"claude-stub","system":[{"text":"stub-system","cache_control":{"type":"ephemeral"}}],"tools":[{"name":"Bash","description":"run a shell command","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hello"}]}`)
	resp, err := http.Post(base+"/v1/messages", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "stubclaude: post: %v\n", err)
		os.Exit(1)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}
