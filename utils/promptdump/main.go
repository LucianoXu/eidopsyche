// Package main implements promptdump, a dev utility that captures the
// Anthropic Messages API request body Claude Code sends, so the verbatim
// default system prompt can be inspected under any combination of claude
// CLI flags. See docs/superpowers/specs/2026-05-11-utils-promptdump-design.md.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
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

// buildEnvelopeMap builds the wrapper map captured alongside the
// request body. Valid JSON body lands under "request"; malformed
// body falls back to "raw_body" + "parse_error" so output is always
// usable. Used by buildEnvelope (JSON output) and renderMarkdown
// (human-readable output).
func buildEnvelopeMap(meta envelopeMeta, body []byte) (map[string]any, error) {
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
	return out, nil
}

// buildEnvelope serializes meta + body into the final pretty-printed
// JSON envelope. Thin wrapper around buildEnvelopeMap.
func buildEnvelope(meta envelopeMeta, body []byte) ([]byte, error) {
	m, err := buildEnvelopeMap(meta, body)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(m, "", "  ")
}

// renderMarkdown formats a parsed envelope as a human-readable
// Markdown document. Companion to buildEnvelope (which produces the
// canonical JSON). Sections — metadata, request config, system
// prompt, tools, first user message — degrade gracefully when
// fields are absent; missing fields yield empty sections rather
// than errors.
func renderMarkdown(env map[string]any) (string, error) {
	var b strings.Builder

	b.WriteString("# promptdump capture\n\n")
	if v, ok := env["captured_at"].(string); ok {
		fmt.Fprintf(&b, "**Captured at:** %s\n", v)
	}
	if v, ok := env["claude_version"].(string); ok {
		fmt.Fprintf(&b, "**Claude version:** %s\n", v)
	}
	if v, ok := env["claude_args"].([]any); ok {
		fmt.Fprintf(&b, "**Claude args:** `%s`\n", joinArgs(v))
	}
	if h, ok := env["host"].(map[string]any); ok {
		platform, _ := h["platform"].(string)
		cwd, _ := h["cwd"].(string)
		fmt.Fprintf(&b, "**Host:** %s · cwd=`%s`\n", platform, cwd)
	}
	b.WriteString("\n")

	req, _ := env["request"].(map[string]any)
	if req == nil {
		if raw, ok := env["raw_body"].(string); ok {
			b.WriteString("## Raw body (failed to parse as JSON)\n\n```\n")
			b.WriteString(raw)
			b.WriteString("\n```\n")
		}
		return b.String(), nil
	}

	b.WriteString("## Request\n\n")
	if v, ok := req["model"].(string); ok {
		fmt.Fprintf(&b, "- **Model:** `%s`\n", v)
	}
	if v, ok := req["max_tokens"].(float64); ok {
		fmt.Fprintf(&b, "- **Max tokens:** %d\n", int(v))
	}
	if v, ok := req["stream"].(bool); ok {
		fmt.Fprintf(&b, "- **Stream:** %v\n", v)
	}
	b.WriteString("\n")

	sys, _ := req["system"].([]any)
	fmt.Fprintf(&b, "## System prompt (%d segments)\n\n", len(sys))
	for i, seg := range sys {
		segMap, _ := seg.(map[string]any)
		header := fmt.Sprintf("### Segment %d", i+1)
		if cc, ok := segMap["cache_control"]; ok {
			ccBytes, _ := json.Marshal(cc)
			header += fmt.Sprintf(" — `cache_control: %s`", string(ccBytes))
		}
		b.WriteString(header + "\n\n")
		text, _ := segMap["text"].(string)
		b.WriteString("```\n")
		b.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
	}

	tools, _ := req["tools"].([]any)
	fmt.Fprintf(&b, "## Tools (%d)\n\n", len(tools))
	for _, t := range tools {
		tm, _ := t.(map[string]any)
		name, _ := tm["name"].(string)
		desc, _ := tm["description"].(string)
		fmt.Fprintf(&b, "- **`%s`** — %s\n", name, firstNonEmptyLine(desc))
	}
	if len(tools) > 0 {
		b.WriteString("\n<details><summary>Full tool schemas</summary>\n\n")
		b.WriteString("```json\n")
		toolsJSON, _ := json.MarshalIndent(tools, "", "  ")
		b.Write(toolsJSON)
		b.WriteString("\n```\n\n</details>\n\n")
	}

	messages, _ := req["messages"].([]any)
	if len(messages) > 0 {
		b.WriteString("## First user message\n\n")
		msg, _ := messages[0].(map[string]any)
		switch content := msg["content"].(type) {
		case string:
			b.WriteString("```\n")
			b.WriteString(content)
			if !strings.HasSuffix(content, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("```\n")
		case []any:
			for _, part := range content {
				pm, _ := part.(map[string]any)
				typ, _ := pm["type"].(string)
				switch typ {
				case "text":
					txt, _ := pm["text"].(string)
					b.WriteString("```\n")
					b.WriteString(txt)
					if !strings.HasSuffix(txt, "\n") {
						b.WriteString("\n")
					}
					b.WriteString("```\n")
				default:
					mediaType, _ := pm["media_type"].(string)
					fmt.Fprintf(&b, "_[%s%s]_\n", typ, formatMediaType(mediaType))
				}
			}
		}
	}

	return b.String(), nil
}

// joinArgs renders a []any of strings as a space-joined argv-like
// line for the metadata block. Non-string entries are %v-formatted.
func joinArgs(args []any) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if s, ok := a.(string); ok {
			parts = append(parts, s)
		} else {
			parts = append(parts, fmt.Sprintf("%v", a))
		}
	}
	return strings.Join(parts, " ")
}

// firstNonEmptyLine returns the first non-empty trimmed line of s.
// Used to summarize tool descriptions in the bullet list.
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			return t
		}
	}
	return ""
}

// formatMediaType adds a " (type)" suffix if non-empty; otherwise
// returns empty. Used in user-message rendering for non-text parts.
func formatMediaType(s string) string {
	if s == "" {
		return ""
	}
	return " " + s
}

// writeJob is one filesystem write produced by dispatchOutput.
// An empty path means "write to stdout"; main() interprets it.
type writeJob struct {
	path    string
	content []byte
}

// dispatchOutput decides what files to write based on outPath's
// extension. Pure (no filesystem side effects); main() iterates the
// returned jobs.
//
// Rules:
//
//	outPath == ""        → [{stdout, jsonBytes}]
//	ext == ".json"       → [{outPath, jsonBytes}]
//	ext == ".md"         → [{outPath, markdown(env)}]
//	anything else        → [{outPath+".json", jsonBytes}, {outPath+".md", markdown(env)}]
func dispatchOutput(jsonBytes []byte, env map[string]any, outPath string) ([]writeJob, error) {
	if outPath == "" {
		return []writeJob{{path: "", content: jsonBytes}}, nil
	}
	switch filepath.Ext(outPath) {
	case ".json":
		return []writeJob{{path: outPath, content: jsonBytes}}, nil
	case ".md":
		md, err := renderMarkdown(env)
		if err != nil {
			return nil, err
		}
		return []writeJob{{path: outPath, content: []byte(md)}}, nil
	default:
		md, err := renderMarkdown(env)
		if err != nil {
			return nil, err
		}
		return []writeJob{
			{path: outPath + ".json", content: jsonBytes},
			{path: outPath + ".md", content: []byte(md)},
		}, nil
	}
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
// return the serialized envelope JSON together with the parsed envelope
// map so callers (e.g. the markdown renderer) don't re-parse.
func runCapture(ctx context.Context, opts runOpts) ([]byte, map[string]any, error) {
	if opts.Prompt == "" {
		opts.Prompt = "ping"
	}
	if opts.NoPOSTAfter == 0 {
		opts.NoPOSTAfter = 8 * time.Second
	}

	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return nil, nil, fmt.Errorf("claude binary not on PATH: %w", err)
	}
	claudeVer := probeClaudeVersion(claudePath)

	srv := newCaptureServer()
	srv.verbose = opts.Verbose
	baseURL, shutdownProxy, err := srv.listen()
	if err != nil {
		return nil, nil, err
	}
	defer shutdownProxy(context.Background())

	args := append([]string{}, opts.ExtraArgs...)
	args = append(args, "-p", opts.Prompt)

	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "promptdump: spawning %s %s\n", claudePath, strings.Join(args, " "))
		fmt.Fprintf(os.Stderr, "promptdump: proxy at %s\n", baseURL)
	}

	cmd := exec.CommandContext(ctx, claudePath, args...)
	// Strip any pre-existing ANTHROPIC_* values from the inherited env
	// before appending our overrides. POSIX execve allows duplicate
	// env keys and most libc implementations resolve to the first
	// occurrence, so naive `append(os.Environ(), "ANTHROPIC_*=...")`
	// can let a developer's existing ANTHROPIC_BASE_URL win and bypass
	// our proxy entirely.
	cmd.Env = append(filterEnv(os.Environ(), "ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY"),
		"ANTHROPIC_BASE_URL="+baseURL,
		"ANTHROPIC_API_KEY=sk-dummy-promptdump",
	)
	stderrBuf := &strings.Builder{}
	cmd.Stderr = stderrBuf
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("spawn claude: %w", err)
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
		return nil, nil, fmt.Errorf("claude did not POST /v1/messages within %s; stderr:\n%s",
			opts.NoPOSTAfter, stderrBuf.String())
	case <-ctx.Done():
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
		return nil, nil, ctx.Err()
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
	envMap, err := buildEnvelopeMap(meta, srv.captured)
	if err != nil {
		return nil, nil, err
	}
	jsonBytes, err := json.MarshalIndent(envMap, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return jsonBytes, envMap, nil
}

// filterEnv returns env with any entries whose key matches one of names
// removed. Used to dedup env before appending overrides for the claude
// subprocess.
func filterEnv(env []string, names ...string) []string {
	skip := make(map[string]struct{}, len(names))
	for _, n := range names {
		skip[n] = struct{}{}
	}
	out := env[:0:0]
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		if _, drop := skip[kv[:eq]]; drop {
			continue
		}
		out = append(out, kv)
	}
	return out
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

// parseFlags parses promptdump's command-line surface. argv[0] is the
// program name; argv[1:] is processed. Anything after a literal "--"
// is collected verbatim into opts.ExtraArgs and passed through to
// claude. Returns the opts, the output path ("" means stdout), and an
// error suitable for printing + exiting non-zero.
func parseFlags(argv []string) (opts runOpts, outPath string, err error) {
	fs := flag.NewFlagSet(argv[0], flag.ContinueOnError)
	prompt := fs.String("p", "ping", "prompt passed to claude as -p <prompt>")
	outFlag := fs.String("o", "", "write captured envelope JSON to this file (default: stdout)")
	verbose := fs.Bool("v", false, "verbose: log proxy traffic and claude stderr")
	keepGoing := fs.Bool("keep-going", false, "do not SIGTERM claude after capture")

	// Find the -- separator manually so flag.Parse doesn't try to
	// interpret claude's flags.
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
		return runOpts{}, "", err
	}

	return runOpts{
		Prompt:      *prompt,
		ExtraArgs:   extra,
		NoPOSTAfter: 8 * time.Second,
		KeepGoing:   *keepGoing,
		Verbose:     *verbose,
	}, *outFlag, nil
}

func main() {
	opts, outPath, err := parseFlags(os.Args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "promptdump: %v\n", err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	jsonBytes, envMap, err := runCapture(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "promptdump: %v\n", err)
		os.Exit(1)
	}

	jobs, err := dispatchOutput(jsonBytes, envMap, outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "promptdump: %v\n", err)
		os.Exit(1)
	}

	var wrote []string
	for _, j := range jobs {
		if j.path == "" {
			os.Stdout.Write(j.content)
			os.Stdout.Write([]byte("\n"))
			continue
		}
		if err := os.WriteFile(j.path, j.content, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "promptdump: write %s: %v\n", j.path, err)
			os.Exit(1)
		}
		wrote = append(wrote, j.path)
	}
	if len(wrote) > 0 {
		fmt.Fprintf(os.Stderr, "promptdump: wrote %s\n", strings.Join(wrote, ", "))
	}
}
