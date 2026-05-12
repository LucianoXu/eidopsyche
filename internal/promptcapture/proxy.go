package promptcapture

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// maxBodyBytes caps the request body the capture handler will accept.
// Claude Code's observed payload is ~200 KB; 16 MB is a generous ceiling
// that still defends against runaway memory if a future version starts
// shipping huge payloads.
const maxBodyBytes = 16 << 20

// captureServer is the HTTP server claude POSTs to (via ANTHROPIC_BASE_URL).
// It captures the first body sent to /v1/messages and responds with a
// minimal valid SSE 200 so claude's SDK can finalize the stream and exit.
type captureServer struct {
	mu       sync.Mutex
	captured []byte
	done     chan struct{}
	// verbose, if true, logs every HTTP path the server receives to
	// os.Stderr. Plumbed from Opts.Verbose.
	verbose bool
}

func newCaptureServer() *captureServer { return &captureServer{done: make(chan struct{})} }

func (s *captureServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", s.handleMessages)
	mux.HandleFunc("/", s.handleOther)
	return mux
}

func (s *captureServer) handleMessages(w http.ResponseWriter, r *http.Request) {
	if s.verbose {
		fmt.Fprintf(os.Stderr, "promptcapture: proxy %s %s\n", r.Method, r.URL.Path)
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if len(body) > maxBodyBytes {
		http.Error(w, "request body exceeds 16 MB cap", http.StatusRequestEntityTooLarge)
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
	if s.verbose {
		fmt.Fprintf(os.Stderr, "promptcapture: proxy %s %s (stub)\n", r.Method, r.URL.Path)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "{}")
}

// minimalSSE is the smallest event stream the Anthropic streaming parser
// accepts as a complete assistant turn. Used to terminate claude after
// the first /v1/messages POST so the child process exits cleanly.
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

// listen binds the capture server to 127.0.0.1 on a kernel-assigned
// port and starts serving in a goroutine. Returns the base URL (e.g.
// "http://127.0.0.1:48273") and a shutdown function suitable for
// `defer shutdown(ctx)`.
func (s *captureServer) listen() (addr string, shutdown func(context.Context) error, err error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("listen: %w", err)
	}
	httpSrv := &http.Server{
		Handler:           s.handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = httpSrv.Serve(lis) }()
	port := lis.Addr().(*net.TCPAddr).Port
	return fmt.Sprintf("http://127.0.0.1:%d", port), httpSrv.Shutdown, nil
}
