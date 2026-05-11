// Package main implements promptdump, a dev utility that captures the
// Anthropic Messages API request body Claude Code sends, so the verbatim
// default system prompt can be inspected under any combination of claude
// CLI flags. See docs/superpowers/specs/2026-05-11-utils-promptdump-design.md.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// maxBodyBytes caps the request body the capture handler will accept.
// Claude Code's observed payload is ~200 KB (probe 2026-05-11). 16 MB
// is a generous ceiling that still defends against runaway memory if
// a future version starts shipping huge payloads.
const maxBodyBytes = 16 << 20

// captureServer is the HTTP server claude POSTs to (via ANTHROPIC_BASE_URL).
// It captures the first body sent to /v1/messages* and responds with a
// minimal valid SSE 200 so claude's SDK can finalize the stream and exit.
type captureServer struct {
	mu       sync.Mutex
	captured []byte
	done     chan struct{}
	// verbose, if true, logs every HTTP path the server receives to
	// os.Stderr. Plumbed from runOpts.Verbose.
	verbose bool
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
	if s.verbose {
		fmt.Fprintf(os.Stderr, "promptdump: proxy %s %s\n", r.Method, r.URL.Path)
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
		fmt.Fprintf(os.Stderr, "promptdump: proxy %s %s (stub)\n", r.Method, r.URL.Path)
	}
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
// (real http.Server on a random port) lives in listen().
func (s *captureServer) start(_ testing.TB) *httptest.Server {
	return httptest.NewServer(s.handler())
}

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
	go func() {
		_ = httpSrv.Serve(lis)
	}()
	port := lis.Addr().(*net.TCPAddr).Port
	return fmt.Sprintf("http://127.0.0.1:%d", port), httpSrv.Shutdown, nil
}

// envelopeMeta is the wrapper metadata captured alongside the request body.
// Its JSON form is the top-level object promptdump writes out.
type envelopeMeta struct {
	CapturedAt    time.Time // formatted into captured_at by buildEnvelope
	ClaudeVersion string
	ClaudePath    string
	ClaudeArgs    []string
	Host          hostInfo
}

// hostInfo captures the host-side context that influences the dynamic
// segments of the system prompt (cwd, platform).
type hostInfo struct {
	Platform string `json:"platform"`
	CWD      string `json:"cwd"`
}

// buildEnvelope serializes meta + body into the final pretty-printed JSON
// envelope. If body parses as JSON, it lands under "request"; otherwise
// the raw bytes are stringified under "raw_body" and a parse-error note
// is added under "parse_error".
func buildEnvelope(meta envelopeMeta, body []byte) ([]byte, error) {
	out := map[string]any{
		"captured_at":    meta.CapturedAt.UTC().Format(time.RFC3339),
		"claude_version": meta.ClaudeVersion,
		"claude_path":    meta.ClaudePath,
		"claude_args":    meta.ClaudeArgs,
		"host":           meta.Host,
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err == nil {
		out["request"] = parsed
	} else {
		out["raw_body"] = string(body)
		out["parse_error"] = err.Error()
	}
	return json.MarshalIndent(out, "", "  ")
}

// runOpts collects the parameters of one capture run.
type runOpts struct {
	Prompt      string        // the -p argument to claude (default "ping")
	ExtraArgs   []string      // appended to claude's argv before -p
	NoPOSTAfter time.Duration // kill claude if no /v1/messages by then
	KeepGoing   bool          // do not terminate claude after capture
	Verbose     bool          // log proxy traffic + claude stderr
}

// runCapture orchestrates one full capture cycle: bring up the proxy,
// spawn claude with env overrides, wait for capture, terminate child,
// return the serialized envelope JSON.
func runCapture(ctx context.Context, opts runOpts) ([]byte, error) {
	if opts.Prompt == "" {
		opts.Prompt = "ping"
	}
	if opts.NoPOSTAfter == 0 {
		opts.NoPOSTAfter = 8 * time.Second
	}

	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("claude binary not on PATH: %w", err)
	}
	claudeVer := probeClaudeVersion(claudePath)

	srv := newCaptureServer()
	srv.verbose = opts.Verbose
	baseURL, shutdownProxy, err := srv.listen()
	if err != nil {
		return nil, err
	}
	defer shutdownProxy(context.Background())

	args := append([]string{}, opts.ExtraArgs...)
	args = append(args, "-p", opts.Prompt)

	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "promptdump: spawning %s %s\n", claudePath, strings.Join(args, " "))
		fmt.Fprintf(os.Stderr, "promptdump: proxy at %s\n", baseURL)
	}

	cmd := exec.CommandContext(ctx, claudePath, args...)
	cmd.Env = append(os.Environ(),
		"ANTHROPIC_BASE_URL="+baseURL,
		"ANTHROPIC_API_KEY=sk-dummy-promptdump",
	)
	stderrBuf := &strings.Builder{}
	cmd.Stderr = stderrBuf
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn claude: %w", err)
	}

	noPOSTTimer := time.NewTimer(opts.NoPOSTAfter)
	defer noPOSTTimer.Stop()

	select {
	case <-srv.done:
		// captured; let claude exit on its own (it should, because we
		// returned a complete SSE stream) — but enforce a short grace.
		if !opts.KeepGoing {
			grace := time.NewTimer(3 * time.Second)
			waitCh := make(chan error, 1)
			go func() { waitCh <- cmd.Wait() }()
			select {
			case <-waitCh:
			case <-grace.C:
				_ = cmd.Process.Signal(syscall.SIGTERM)
				<-waitCh
			}
			grace.Stop()
		} else {
			_ = cmd.Wait()
		}
	case <-noPOSTTimer.C:
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
		return nil, fmt.Errorf("claude did not POST /v1/messages within %s; stderr:\n%s",
			opts.NoPOSTAfter, stderrBuf.String())
	case <-ctx.Done():
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
		return nil, ctx.Err()
	}

	if opts.Verbose && stderrBuf.Len() > 0 {
		fmt.Fprintf(os.Stderr, "promptdump: claude stderr:\n%s\n", stderrBuf.String())
	}

	cwd, _ := os.Getwd()
	meta := envelopeMeta{
		CapturedAt:    time.Now().UTC(),
		ClaudeVersion: claudeVer,
		ClaudePath:    claudePath,
		ClaudeArgs:    args,
		Host:          hostInfo{Platform: runtime.GOOS, CWD: cwd},
	}
	return buildEnvelope(meta, srv.captured)
}

// probeClaudeVersion runs `claude --version` with a short timeout and
// returns the trimmed stdout, or the literal "unknown" on any failure.
func probeClaudeVersion(claudePath string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, claudePath, "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func main() {
	// Wired up in a later task. For now main() just announces itself so
	// `go run .` continues to do something visible.
	fmt.Println("promptdump: not yet implemented")
}
