// Package main implements promptdump, a dev utility that captures the
// Anthropic Messages API request body Claude Code sends, so the verbatim
// default system prompt can be inspected under any combination of claude
// CLI flags. See docs/superpowers/specs/2026-05-11-utils-promptdump-design.md.
package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// captureServer is the HTTP server claude POSTs to (via ANTHROPIC_BASE_URL).
// It captures the first body sent to /v1/messages* and responds with a
// minimal valid SSE 200 so claude's SDK can finalize the stream and exit.
type captureServer struct {
	mu       sync.Mutex
	captured []byte
	done     chan struct{}
}

func newCaptureServer() *captureServer {
	return &captureServer{done: make(chan struct{})}
}

// handler routes the few endpoints claude touches during a print-mode run.
// Only /v1/messages POSTs are captured; everything else returns a tiny
// stub so claude doesn't bail on a 404 cascade during startup.
func (s *captureServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", s.handleMessages)
	mux.HandleFunc("/", s.handleOther)
	return mux
}

func (s *captureServer) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	first := s.captured == nil
	if first {
		s.captured = body
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(minimalSSE)); err != nil {
		return
	}
	if first {
		close(s.done)
	}
}

func (s *captureServer) handleOther(w http.ResponseWriter, r *http.Request) {
	// Permissive stub for ancillary endpoints (model list, MCP registry,
	// telemetry). Most claude code paths tolerate a 404 here; we return
	// 200 with an empty JSON object to be maximally polite.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "{}")
}

// minimalSSE is the smallest event stream the Anthropic streaming parser
// will accept as a complete assistant turn. Used to terminate claude
// after the first /v1/messages POST so the child process exits cleanly.
const minimalSSE = "" +
	"event: message_start\n" +
	`data: {"type":"message_start","message":{"id":"msg_promptdump","type":"message","role":"assistant","model":"capture","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n\n" +
	"event: content_block_start\n" +
	`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
	"event: content_block_stop\n" +
	`data: {"type":"content_block_stop","index":0}` + "\n\n" +
	"event: message_delta\n" +
	`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}` + "\n\n" +
	"event: message_stop\n" +
	`data: {"type":"message_stop"}` + "\n\n"

// start is a test-helper: it starts the server via httptest and returns
// the *httptest.Server so the test can read its URL. Production wiring
// (real http.Server on a random port) lives in listen(), added later.
func (s *captureServer) start(_ testing.TB) *httptest.Server {
	return httptest.NewServer(s.handler())
}

func main() {
	// Wired up in a later task. For now main() just announces itself so
	// `go run .` continues to do something visible.
	fmt.Println("promptdump: not yet implemented")
}
