# `forge prompt-dump` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `eidos forge prompt-dump <name>` — a one-shot capture of the verbatim `/v1/messages` request envelope a mind-form's `claude` would send right now, paralleling `forge watch`'s response-side view.

**Architecture:** Promote the existing `utils/promptdump` core into a new `internal/promptcapture` package. Add a host-side cobra command that `docker exec`s an in-container worker; the worker reads the mind-form's `identity.md` + `config.toml`, runs the proxy + claude dance entirely on the container's loopback, and prints a JSON envelope. The host streams the envelope verbatim to stdout, or unmarshals + dual-writes JSON+Markdown when `-o` is given. `utils/promptdump` shrinks to a thin wrapper around `internal/promptcapture` (still GOWORK=off, outside `go.work`).

**Tech Stack:** Go 1.x (project workspace), cobra (CLI), Docker SDK via `internal/forgectl`, stdlib `net/http` + `os/exec` for the proxy/spawn, existing `internal/config` for `config.toml` parsing.

**Spec:** `docs/superpowers/specs/2026-05-12-forge-prompt-dump-design.md`

**In-container paths (canonical):**
- `claude` binary: `/usr/local/bin/claude` (on PATH)
- ontology root / claude cwd: `/eidos/ontology`
- identity: `/eidos/ontology/self/identity.md`
- claude data dir: `/eidos/ontology/.claude` (passed via `CLAUDE_DIR` env)
- gate config: `/eidos/gate/config.toml` (where `[mindform] model = "..."` lives)

---

## Task 1: Scaffold `internal/promptcapture` package (no logic yet)

Stand up the package skeleton + doc.go so subsequent tasks can fill in the moves without import churn.

**Files:**
- Create: `internal/promptcapture/doc.go`

- [ ] **Step 1: Write `doc.go`**

```go
// Package promptcapture intercepts the Anthropic Messages API request body
// Claude Code sends, so callers can inspect the verbatim default system
// prompt, tool catalogue, and first user message under arbitrary claude
// flag combinations.
//
// The package is shared by two surfaces:
//
//   - cmd/eidos/forge.prompt-dump (the in-container worker invoked by
//     "eidos forge prompt-dump <name>" against a running mind-form).
//   - utils/promptdump (the dev-only host shim, kept outside go.work).
//
// Both surfaces drive promptcapture.Run, which:
//
//  1. binds a loopback HTTP server on 127.0.0.1:0,
//  2. spawns claude with ANTHROPIC_BASE_URL=http://127.0.0.1:<port> and a
//     dummy API key,
//  3. captures the first POST /v1/messages body,
//  4. returns a minimal-valid SSE response so claude exits cleanly,
//  5. assembles a typed Envelope with metadata + parsed request body.
//
// See docs/superpowers/specs/2026-05-12-forge-prompt-dump-design.md for
// the design rationale and the alternatives considered.
package promptcapture
```

- [ ] **Step 2: Verify the package builds**

Run: `go build ./internal/promptcapture/...`
Expected: exits 0 with no output.

- [ ] **Step 3: Commit**

```bash
git add internal/promptcapture/doc.go
git commit -m "feat(promptcapture): scaffold package for shared capture core"
```

---

## Task 2: Move proxy + minimal SSE into `internal/promptcapture/proxy.go` (TDD)

Pull the `captureServer` + `minimalSSE` constant out of `utils/promptdump/main.go` into the new package, with a unit test covering: first-POST capture, second-POST ignored, non-POST rejected.

**Files:**
- Create: `internal/promptcapture/proxy.go`
- Create: `internal/promptcapture/proxy_test.go`

- [ ] **Step 1: Write the failing test (`proxy_test.go`)**

```go
package promptcapture

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestProxyCapturesFirstPOST(t *testing.T) {
	srv := newCaptureServer()
	addr, shutdown, err := srv.listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer shutdown(context.Background())

	resp, err := http.Post(addr+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"x"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "message_start") {
		t.Fatalf("expected minimal SSE, got: %s", body)
	}
	select {
	case <-srv.done:
	default:
		t.Fatal("done channel not closed after first POST")
	}
	if !bytes.Contains(srv.captured, []byte(`"model":"x"`)) {
		t.Fatalf("captured=%q", srv.captured)
	}
}

func TestProxyIgnoresSecondPOST(t *testing.T) {
	srv := newCaptureServer()
	addr, shutdown, err := srv.listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer shutdown(context.Background())

	_, _ = http.Post(addr+"/v1/messages", "application/json",
		strings.NewReader(`{"first":true}`))
	_, _ = http.Post(addr+"/v1/messages", "application/json",
		strings.NewReader(`{"second":true}`))

	if !bytes.Contains(srv.captured, []byte(`"first":true`)) {
		t.Fatalf("first body not retained: %q", srv.captured)
	}
	if bytes.Contains(srv.captured, []byte(`"second":true`)) {
		t.Fatalf("second body leaked into captured: %q", srv.captured)
	}
}

func TestProxyRejectsNonPOST(t *testing.T) {
	srv := newCaptureServer()
	addr, shutdown, err := srv.listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer shutdown(context.Background())

	resp, err := http.Get(addr + "/v1/messages")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/promptcapture/ -run TestProxy`
Expected: FAIL — `undefined: newCaptureServer`.

- [ ] **Step 3: Write `proxy.go` (port from `utils/promptdump/main.go` lines 26–138 and the `minimalSSE` constant at lines 108–118)**

```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/promptcapture/ -run TestProxy -v`
Expected: PASS for all three tests.

- [ ] **Step 5: Commit**

```bash
git add internal/promptcapture/proxy.go internal/promptcapture/proxy_test.go
git commit -m "feat(promptcapture): port loopback proxy + minimal SSE"
```

---

## Task 3: Move envelope types + Markdown rendering into `internal/promptcapture/envelope.go` (TDD)

Move the JSON envelope construction (`envelopeMeta`, `hostInfo`, `buildEnvelopeMap`, `buildEnvelope`, `renderMarkdown` and its helpers) into the new package. Add a `CapturedFrom` struct for the new mind-form context block.

**Files:**
- Create: `internal/promptcapture/envelope.go`
- Create: `internal/promptcapture/envelope_test.go`

- [ ] **Step 1: Write the failing test**

```go
package promptcapture

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildEnvelopeMapAttachesCapturedFrom(t *testing.T) {
	meta := EnvelopeMeta{
		CapturedAt:    time.Unix(0, 0).UTC(),
		ClaudeVersion: "test",
		ClaudePath:    "/x/claude",
		ClaudeArgs:    []string{"-p", "ping"},
		Host:          HostInfo{Platform: "linux", CWD: "/eidos/ontology"},
		CapturedFrom: CapturedFrom{
			Mindform:     "alice",
			OntologyRoot: "/eidos/ontology",
			Model:        "sonnet",
			IdentityPath: "self/identity.md",
			Bare:         false,
		},
	}
	body := []byte(`{"system":[{"text":"hi"}]}`)
	m, err := BuildEnvelopeMap(meta, body)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cf, ok := m["captured_from"].(map[string]any)
	if !ok {
		t.Fatalf("captured_from missing or wrong type: %v", m["captured_from"])
	}
	if cf["mindform"] != "alice" || cf["model"] != "sonnet" || cf["bare"] != false {
		t.Fatalf("captured_from values: %v", cf)
	}
	req, _ := m["request"].(map[string]any)
	if req == nil {
		t.Fatalf("request missing")
	}
}

func TestRenderMarkdownIncludesCapturedFromHeader(t *testing.T) {
	meta := EnvelopeMeta{
		CapturedAt: time.Unix(0, 0).UTC(),
		CapturedFrom: CapturedFrom{
			Mindform: "alice", Model: "sonnet",
			IdentityPath: "self/identity.md",
		},
	}
	m, err := BuildEnvelopeMap(meta, []byte(`{"system":[]}`))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	md, err := RenderMarkdown(m)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"Captured from", "alice", "sonnet", "self/identity.md"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestBuildEnvelopeMapFallsBackToRawBody(t *testing.T) {
	meta := EnvelopeMeta{CapturedAt: time.Unix(0, 0).UTC()}
	m, err := BuildEnvelopeMap(meta, []byte(`not-json`))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, ok := m["request"]; ok {
		t.Fatalf("request should be absent for unparseable body")
	}
	if m["raw_body"] != "not-json" {
		t.Fatalf("raw_body=%v", m["raw_body"])
	}
	if _, ok := m["parse_error"].(string); !ok {
		t.Fatalf("parse_error missing: %v", m)
	}
}

func TestRoundTripJSON(t *testing.T) {
	meta := EnvelopeMeta{CapturedAt: time.Unix(0, 0).UTC()}
	body := []byte(`{"system":[],"tools":[]}`)
	js, err := BuildEnvelope(meta, body)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(js, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/promptcapture/ -run 'TestBuildEnvelope|TestRender|TestRoundTrip'`
Expected: FAIL — `undefined: BuildEnvelopeMap`, etc.

- [ ] **Step 3: Write `envelope.go` (port + extend)**

Port everything from `utils/promptdump/main.go` lines 140–439 (`envelopeMeta`, `hostInfo`, `buildEnvelopeMap`, `buildEnvelope`, `renderMarkdown`, `joinArgs`, `firstNonEmptyLine`, `renderOtherRequestFields`, `renderToolDetail`, `formatMediaType`), with these changes:

1. Export the types and functions: `envelopeMeta` → `EnvelopeMeta`, `hostInfo` → `HostInfo`, `buildEnvelopeMap` → `BuildEnvelopeMap`, `buildEnvelope` → `BuildEnvelope`, `renderMarkdown` → `RenderMarkdown`. Internal helpers (`joinArgs`, `firstNonEmptyLine`, `renderOtherRequestFields`, `renderToolDetail`, `formatMediaType`) stay unexported.
2. Add a `CapturedFrom` struct and embed it in `EnvelopeMeta`:

```go
// CapturedFrom records which mind-form context produced the envelope.
// All fields are optional — utils/promptdump (no mind-form) leaves the
// struct zero-valued; the in-container forge worker fills it in.
type CapturedFrom struct {
	Mindform     string `json:"mindform"`      // mind-form name (host wrapper supplies via --mindform-name)
	OntologyRoot string `json:"ontology_root"` // e.g. /eidos/ontology
	Model        string `json:"model"`         // resolved model from config.toml; "" if unset
	IdentityPath string `json:"identity_path"` // path relative to OntologyRoot; "" if no identity
	Bare         bool   `json:"bare"`          // true when --append-system-prompt was omitted
}

type EnvelopeMeta struct {
	CapturedAt    time.Time
	ClaudeVersion string
	ClaudePath    string
	ClaudeArgs    []string
	Host          HostInfo
	CapturedFrom  CapturedFrom // zero-valued for utils/promptdump callers
}
```

3. In `BuildEnvelopeMap`, add `captured_from` to the raw map, but **only when at least one field is non-zero**:

```go
raw := map[string]any{
    "captured_at":    meta.CapturedAt.UTC().Format(time.RFC3339),
    "claude_version": meta.ClaudeVersion,
    "claude_path":    meta.ClaudePath,
    "claude_args":    meta.ClaudeArgs,
    "host":           meta.Host,
}
if meta.CapturedFrom != (CapturedFrom{}) {
    raw["captured_from"] = meta.CapturedFrom
}
// ... rest unchanged (parse body → request or raw_body+parse_error)
```

4. In `RenderMarkdown`, add a "Captured from" section between metadata and the "## Request" section:

```go
if cf, ok := env["captured_from"].(map[string]any); ok {
    b.WriteString("**Captured from:** ")
    var parts []string
    if v, _ := cf["mindform"].(string); v != "" {
        parts = append(parts, "mindform="+v)
    }
    if v, _ := cf["model"].(string); v != "" {
        parts = append(parts, "model="+v)
    }
    if v, _ := cf["identity_path"].(string); v != "" {
        parts = append(parts, "identity="+v)
    }
    if v, _ := cf["bare"].(bool); v {
        parts = append(parts, "bare=true")
    }
    if v, _ := cf["ontology_root"].(string); v != "" {
        parts = append(parts, "ontology_root="+v)
    }
    b.WriteString(strings.Join(parts, " · "))
    b.WriteString("\n")
}
```

The "Captured from:" line goes after the existing metadata block (Captured at, Claude version, etc.) and before the blank `\n` separator. Concretely, insert it between the existing `host` block and the trailing `b.WriteString("\n")` at the end of the metadata section.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/promptcapture/ -v`
Expected: All `TestBuildEnvelope*`, `TestRender*`, `TestRoundTripJSON` and the earlier `TestProxy*` tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/promptcapture/envelope.go internal/promptcapture/envelope_test.go
git commit -m "feat(promptcapture): port envelope types + add captured_from"
```

---

## Task 4: Add `WriteOutputs` for extension-driven dual-write (TDD)

Move `dispatchOutput` + the `writeJob` machinery from `utils/promptdump/main.go` into `internal/promptcapture/envelope.go` as the exported `WriteOutputs`. Host wrapper will use it for `-o` handling.

**Files:**
- Modify: `internal/promptcapture/envelope.go`
- Create: `internal/promptcapture/output_test.go`

- [ ] **Step 1: Write the failing test**

```go
package promptcapture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteOutputs_JSONExtension(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.json")
	meta := EnvelopeMeta{CapturedAt: time.Unix(0, 0).UTC()}
	m, _ := BuildEnvelopeMap(meta, []byte(`{"system":[]}`))
	wrote, err := WriteOutputs(m, out)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(wrote) != 1 || wrote[0] != out {
		t.Fatalf("wrote=%v", wrote)
	}
	b, _ := os.ReadFile(out)
	if !strings.Contains(string(b), `"captured_at"`) {
		t.Fatalf("json content unexpected: %s", b)
	}
}

func TestWriteOutputs_MDExtension(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.md")
	meta := EnvelopeMeta{CapturedAt: time.Unix(0, 0).UTC()}
	m, _ := BuildEnvelopeMap(meta, []byte(`{"system":[]}`))
	wrote, err := WriteOutputs(m, out)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(wrote) != 1 || wrote[0] != out {
		t.Fatalf("wrote=%v", wrote)
	}
	b, _ := os.ReadFile(out)
	if !strings.Contains(string(b), "# promptdump capture") {
		t.Fatalf("md content unexpected: %s", b)
	}
}

func TestWriteOutputs_BareBasenameWritesBoth(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "snap")
	meta := EnvelopeMeta{CapturedAt: time.Unix(0, 0).UTC()}
	m, _ := BuildEnvelopeMap(meta, []byte(`{"system":[]}`))
	wrote, err := WriteOutputs(m, base)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(wrote) != 2 {
		t.Fatalf("wrote=%v", wrote)
	}
	if _, err := os.Stat(base + ".json"); err != nil {
		t.Fatalf("missing json: %v", err)
	}
	if _, err := os.Stat(base + ".md"); err != nil {
		t.Fatalf("missing md: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/promptcapture/ -run TestWriteOutputs`
Expected: FAIL — `undefined: WriteOutputs`.

- [ ] **Step 3: Add `WriteOutputs` to `envelope.go`**

```go
// WriteOutputs writes envelope m to one or two files on disk based on
// outPath's extension:
//
//	ext == ".json"  → write JSON to outPath
//	ext == ".md"    → write Markdown to outPath
//	anything else   → write both outPath+".json" and outPath+".md"
//
// Returns the list of paths actually written. Callers handling stdout
// should not pass an empty outPath — this function always writes files.
func WriteOutputs(env map[string]any, outPath string) ([]string, error) {
	if outPath == "" {
		return nil, fmt.Errorf("WriteOutputs: outPath required (caller handles stdout)")
	}
	jsonBytes, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, err
	}
	switch filepath.Ext(outPath) {
	case ".json":
		if err := os.WriteFile(outPath, jsonBytes, 0o644); err != nil {
			return nil, err
		}
		return []string{outPath}, nil
	case ".md":
		md, err := RenderMarkdown(env)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(outPath, []byte(md), 0o644); err != nil {
			return nil, err
		}
		return []string{outPath}, nil
	default:
		md, err := RenderMarkdown(env)
		if err != nil {
			return nil, err
		}
		jsonPath := outPath + ".json"
		mdPath := outPath + ".md"
		if err := os.WriteFile(jsonPath, jsonBytes, 0o644); err != nil {
			return nil, err
		}
		if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
			return nil, err
		}
		return []string{jsonPath, mdPath}, nil
	}
}
```

Add the new imports to `envelope.go`: `"os"`, `"path/filepath"` (and `"fmt"` if not already present).

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/promptcapture/ -v`
Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/promptcapture/envelope.go internal/promptcapture/output_test.go
git commit -m "feat(promptcapture): add WriteOutputs for extension-driven dual-write"
```

---

## Task 5: Add `Run` orchestrator + `Opts` (TDD with a stub claude)

This is the high-level entry point both surfaces call. It takes a binary path + identity + cwd + model + prompt + verbose + timeout, runs the proxy, spawns the binary, captures, and returns an `Envelope`. We test it with a tiny stub binary built on the fly.

**Files:**
- Create: `internal/promptcapture/capture.go`
- Create: `internal/promptcapture/capture_test.go`
- Create: `internal/promptcapture/testdata/stubclaude/main.go` (a small Go program that pretends to be claude)

- [ ] **Step 1: Write the stub claude program**

Create `internal/promptcapture/testdata/stubclaude/main.go`:

```go
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
```

- [ ] **Step 2: Write the failing test**

Create `internal/promptcapture/capture_test.go`:

```go
package promptcapture

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func buildStubClaude(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "stubclaude")
	cmd := exec.Command("go", "build", "-o", out, "./testdata/stubclaude")
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build stub: %v\n%s", err, combined)
	}
	return out
}

func TestRunCapturesEnvelopeViaStub(t *testing.T) {
	stub := buildStubClaude(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	env, err := Run(ctx, Opts{
		ClaudeBin:      stub,
		Cwd:            t.TempDir(),
		IdentityPrompt: "test identity",
		Model:          "sonnet",
		Prompt:         "ping",
		CapturedFrom: CapturedFrom{
			Mindform:     "alice",
			OntologyRoot: "/eidos/ontology",
			Model:        "sonnet",
			IdentityPath: "self/identity.md",
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	req, _ := env["request"].(map[string]any)
	if req == nil {
		t.Fatalf("no request in envelope: %#v", env)
	}
	if req["model"] != "claude-stub" {
		t.Fatalf("request.model=%v", req["model"])
	}
	cf, _ := env["captured_from"].(map[string]any)
	if cf["mindform"] != "alice" {
		t.Fatalf("captured_from.mindform=%v", cf["mindform"])
	}
}

func TestRunReturnsTimeoutWhenStubPostsNothing(t *testing.T) {
	// Build a stub that just exits without POSTing.
	dir := t.TempDir()
	stub := filepath.Join(dir, "silentclaude")
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if out, err := exec.Command("go", "build", "-o", stub, src).CombinedOutput(); err != nil {
		t.Fatalf("build silent: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := Run(ctx, Opts{
		ClaudeBin:   stub,
		Cwd:         t.TempDir(),
		Prompt:      "ping",
		NoPOSTAfter: 500 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
```

(Add `"os"` import for the silent-claude test.)

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/promptcapture/ -run TestRun`
Expected: FAIL — `undefined: Run`, `undefined: Opts`.

- [ ] **Step 4: Write `capture.go`**

```go
package promptcapture

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// Opts configures one capture run. Callers fill in the fields they
// know; defaults are applied inside Run.
type Opts struct {
	// ClaudeBin is the path to the claude executable (or stub in tests).
	// Required.
	ClaudeBin string
	// Cwd is the working directory for the spawned claude. When set,
	// claude picks up any ancestor CLAUDE.md it finds from there.
	Cwd string
	// IdentityPrompt is passed via --append-system-prompt. Empty means
	// omit the flag (equivalent to --bare).
	IdentityPrompt string
	// Model, if non-empty, is passed as --model. Empty → omitted.
	Model string
	// Prompt is the -p argument. Default "ping" when empty.
	Prompt string
	// ExtraArgs are appended after the standard args. Used by
	// utils/promptdump to pass through user-supplied flags after `--`.
	ExtraArgs []string
	// ClaudeDir, if non-empty, is set as CLAUDE_DIR in the spawn env.
	// Used in-container to match the agent-loop's claude data dir.
	ClaudeDir string
	// CapturedFrom is stamped onto the envelope. Zero-valued for the
	// utils/promptdump shim; populated by the in-container worker.
	CapturedFrom CapturedFrom
	// NoPOSTAfter caps how long Run waits for claude to POST a request.
	// Default 10s (vs utils/promptdump's 8s — cold-start claude inside a
	// sleeping mindform needs the headroom).
	NoPOSTAfter time.Duration
	// Verbose mirrors `-v`: log proxy traffic + claude stderr to os.Stderr.
	Verbose bool
}

// Run executes one full capture cycle and returns the assembled
// envelope as a JSON-normalized map. The map is what BuildEnvelope*
// produces — callers serialize to JSON or render to Markdown as needed.
func Run(ctx context.Context, opts Opts) (map[string]any, error) {
	if opts.ClaudeBin == "" {
		return nil, fmt.Errorf("promptcapture: ClaudeBin required")
	}
	if opts.Prompt == "" {
		opts.Prompt = "ping"
	}
	if opts.NoPOSTAfter == 0 {
		opts.NoPOSTAfter = 10 * time.Second
	}
	claudeVer := probeClaudeVersion(ctx, opts.ClaudeBin)

	srv := newCaptureServer()
	srv.verbose = opts.Verbose
	baseURL, shutdownProxy, err := srv.listen()
	if err != nil {
		return nil, err
	}
	defer shutdownProxy(context.Background())

	args := buildClaudeArgs(opts)

	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "promptcapture: spawning %s %s\n", opts.ClaudeBin, strings.Join(args, " "))
		fmt.Fprintf(os.Stderr, "promptcapture: proxy at %s\n", baseURL)
	}

	cmd := exec.CommandContext(ctx, opts.ClaudeBin, args...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildSpawnEnv(baseURL, opts.ClaudeDir)
	stderrBuf := &strings.Builder{}
	cmd.Stderr = stderrBuf
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn %s: %w", opts.ClaudeBin, err)
	}

	noPOSTTimer := time.NewTimer(opts.NoPOSTAfter)
	defer noPOSTTimer.Stop()

	select {
	case <-srv.done:
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
		fmt.Fprintf(os.Stderr, "promptcapture: claude stderr:\n%s\n", stderrBuf.String())
	}

	cwd, _ := os.Getwd()
	meta := EnvelopeMeta{
		CapturedAt:    time.Now().UTC(),
		ClaudeVersion: claudeVer,
		ClaudePath:    opts.ClaudeBin,
		ClaudeArgs:    args,
		Host:          HostInfo{Platform: runtime.GOOS, CWD: cwd},
		CapturedFrom:  opts.CapturedFrom,
	}
	return BuildEnvelopeMap(meta, srv.captured)
}

// buildClaudeArgs constructs the argv slice for the spawned process.
func buildClaudeArgs(opts Opts) []string {
	var args []string
	if opts.IdentityPrompt != "" {
		args = append(args, "--append-system-prompt", opts.IdentityPrompt)
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	args = append(args, "--dangerously-skip-permissions")
	args = append(args, opts.ExtraArgs...)
	args = append(args, "-p", opts.Prompt)
	return args
}

// buildSpawnEnv strips any pre-existing ANTHROPIC_*/CLAUDE_DIR from the
// inherited env (POSIX execve allows duplicate keys and most libc
// implementations resolve to the first occurrence, which would let a
// developer's existing values bypass our proxy or claude_dir).
func buildSpawnEnv(baseURL, claudeDir string) []string {
	env := filterEnv(os.Environ(), "ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "CLAUDE_DIR")
	env = append(env,
		"ANTHROPIC_BASE_URL="+baseURL,
		"ANTHROPIC_API_KEY=sk-dummy-promptdump",
	)
	if claudeDir != "" {
		env = append(env, "CLAUDE_DIR="+claudeDir)
	}
	return env
}

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

func probeClaudeVersion(ctx context.Context, claudePath string) string {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, claudePath, "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/promptcapture/ -v`
Expected: All tests PASS, including `TestRunCapturesEnvelopeViaStub` and `TestRunReturnsTimeoutWhenStubPostsNothing`.

- [ ] **Step 6: Commit**

```bash
git add internal/promptcapture/capture.go internal/promptcapture/capture_test.go internal/promptcapture/testdata/
git commit -m "feat(promptcapture): add Run orchestrator + Opts"
```

---

## Task 6: Shrink `utils/promptdump/main.go` to call `internal/promptcapture.Run`

Replace `utils/promptdump`'s in-tree proxy + envelope code with calls into the new shared package. Verify `utils/promptdump`'s existing tests pass against the wrapper.

**Files:**
- Modify: `utils/promptdump/main.go` (delete most of it; keep only flag parsing + main + output dispatch)
- Modify: `utils/promptdump/main_test.go` (keep the tests that exercise the wrapper; drop tests that covered the now-moved internals — those are reborn under `internal/promptcapture/`)
- Modify: `utils/promptdump/go.mod` — add a `replace` directive pointing at the parent module + a `require` line.

- [ ] **Step 1: Update `utils/promptdump/go.mod`**

Look at the current `utils/promptdump/go.mod`. Add `require` + `replace` so the wrapper can import `github.com/LucianoXu/eidopsyche/internal/promptcapture`. The replace points up two directories to the parent module root.

Example expected content (adjust the module version line to whatever Go uses today; the `replace` is the load-bearing piece):

```go
module github.com/LucianoXu/eidopsyche/utils/promptdump

go 1.22

require github.com/LucianoXu/eidopsyche v0.0.0

replace github.com/LucianoXu/eidopsyche => ../..
```

- [ ] **Step 2: Verify the replace works**

Run: `cd utils/promptdump && GOWORK=off go mod tidy && GOWORK=off go build .`
Expected: builds without error. (`go mod tidy` may add transitive requires; that's fine.)

- [ ] **Step 3: Rewrite `utils/promptdump/main.go` as a thin wrapper**

```go
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
```

- [ ] **Step 4: Trim `utils/promptdump/main_test.go`**

Open `utils/promptdump/main_test.go`. Delete any test that exercised the now-moved internals (`captureServer`, `buildEnvelopeMap`, `renderMarkdown`, `dispatchOutput`, `filterEnv`, `probeClaudeVersion`, `runCapture`, etc.) — those have equivalents under `internal/promptcapture/`. Keep:

- The `parseFlags` test if any (test the wrapper's own flag parsing).
- The end-to-end smoke test (if any) that builds + runs the binary against a real `claude`.

If the file becomes empty after trimming, delete it.

If no `parseFlags` test exists, write one new test that covers the wrapper's flag parsing — this keeps the wrapper covered by its own tests:

```go
package main

import (
	"reflect"
	"testing"
)

func TestParseFlagsSplitsAtDoubleDash(t *testing.T) {
	opts, err := parseFlags([]string{"promptdump", "-p", "hi", "--", "--model", "sonnet"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.Prompt != "hi" {
		t.Errorf("Prompt=%q", opts.Prompt)
	}
	if !reflect.DeepEqual(opts.ExtraArgs, []string{"--model", "sonnet"}) {
		t.Errorf("ExtraArgs=%v", opts.ExtraArgs)
	}
}

func TestParseFlagsDefaults(t *testing.T) {
	opts, err := parseFlags([]string{"promptdump"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.Prompt != "ping" {
		t.Errorf("default Prompt=%q want ping", opts.Prompt)
	}
	if opts.OutPath != "" {
		t.Errorf("default OutPath=%q want empty", opts.OutPath)
	}
}
```

- [ ] **Step 5: Verify the wrapper builds and its remaining tests pass**

Run:
```
cd utils/promptdump && GOWORK=off go build .
GOWORK=off go test -short ./...
```
Expected: builds; tests PASS.

- [ ] **Step 6: Verify the parent workspace still builds**

Run from repo root: `go build ./...` and `go test ./internal/promptcapture/...`
Expected: both succeed.

- [ ] **Step 7: Commit**

```bash
git add utils/promptdump/
git commit -m "refactor(promptdump): shrink utils/promptdump to a thin wrapper over internal/promptcapture"
```

---

## Task 7: Add `--in-container` worker — `eidos forge prompt-dump` (in-container side, TDD)

Register the in-container subcommand that reads identity.md + config.toml, invokes `promptcapture.Run`, and prints the envelope JSON to stdout. Inject the `claudeBin` as a function-level variable so tests can substitute the stub.

**Files:**
- Create: `cmd/eidos/forge/prompt_dump_incontainer.go`
- Create: `cmd/eidos/forge/prompt_dump_incontainer_test.go`
- Modify: `cmd/eidos/forge/incontainer.go` (register the new in-container subcommand)

- [ ] **Step 1: Write the failing test**

Create `cmd/eidos/forge/prompt_dump_incontainer_test.go`:

```go
package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildStubClaude reuses the same approach as the promptcapture tests.
func buildStubClaude(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "stubclaude")
	cmd := exec.Command("go", "build", "-o", out,
		"github.com/LucianoXu/eidopsyche/internal/promptcapture/testdata/stubclaude")
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build stub: %v\n%s", err, combined)
	}
	return out
}

func TestRunPromptDumpInContainer_PopulatesCapturedFrom(t *testing.T) {
	stub := buildStubClaude(t)

	// Lay out a fake in-container filesystem.
	root := t.TempDir()
	ontologyDir := filepath.Join(root, "ontology")
	gateDir := filepath.Join(root, "gate")
	if err := os.MkdirAll(filepath.Join(ontologyDir, "self"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(gateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ontologyDir, "self", "identity.md"),
		[]byte("# Alice\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gateDir, "config.toml"),
		[]byte(`[mindform]`+"\n"+`model = "sonnet"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err := runPromptDumpInContainer(context.Background(), promptDumpInContainerInput{
		ClaudeBin:      stub,
		OntologyRoot:   ontologyDir,
		GateConfigPath: filepath.Join(gateDir, "config.toml"),
		MindformName:   "alice",
		Prompt:         "ping",
		Bare:           false,
		Stdout:         &stdout,
		Stderr:         os.Stderr,
	})
	if err != nil {
		t.Fatalf("runPromptDumpInContainer: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, stdout.String())
	}
	cf, _ := env["captured_from"].(map[string]any)
	if cf == nil {
		t.Fatalf("captured_from missing")
	}
	if cf["mindform"] != "alice" {
		t.Errorf("mindform=%v", cf["mindform"])
	}
	if cf["model"] != "sonnet" {
		t.Errorf("model=%v", cf["model"])
	}
	if cf["identity_path"] != "self/identity.md" {
		t.Errorf("identity_path=%v", cf["identity_path"])
	}
	if cf["bare"] != false {
		t.Errorf("bare=%v", cf["bare"])
	}
}

func TestRunPromptDumpInContainer_BareSkipsIdentity(t *testing.T) {
	stub := buildStubClaude(t)
	root := t.TempDir()
	ontologyDir := filepath.Join(root, "ontology")
	gateDir := filepath.Join(root, "gate")
	_ = os.MkdirAll(filepath.Join(ontologyDir, "self"), 0o755)
	_ = os.MkdirAll(gateDir, 0o755)
	_ = os.WriteFile(filepath.Join(ontologyDir, "self", "identity.md"),
		[]byte("# Should not appear\n"), 0o644)
	_ = os.WriteFile(filepath.Join(gateDir, "config.toml"), []byte(""), 0o644)

	var stdout bytes.Buffer
	err := runPromptDumpInContainer(context.Background(), promptDumpInContainerInput{
		ClaudeBin: stub, OntologyRoot: ontologyDir,
		GateConfigPath: filepath.Join(gateDir, "config.toml"),
		MindformName:   "alice", Bare: true, Stdout: &stdout, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var env map[string]any
	_ = json.Unmarshal(stdout.Bytes(), &env)
	cf, _ := env["captured_from"].(map[string]any)
	if cf["bare"] != true {
		t.Errorf("bare=%v", cf["bare"])
	}
	args, _ := env["claude_args"].([]any)
	for _, a := range args {
		if s, _ := a.(string); s == "--append-system-prompt" {
			t.Fatalf("claude_args contains --append-system-prompt under --bare")
		}
	}
}

func TestRunPromptDumpInContainer_MissingIdentityProceedsWithEmpty(t *testing.T) {
	stub := buildStubClaude(t)
	root := t.TempDir()
	ontologyDir := filepath.Join(root, "ontology")
	gateDir := filepath.Join(root, "gate")
	_ = os.MkdirAll(ontologyDir, 0o755) // no self/ dir
	_ = os.MkdirAll(gateDir, 0o755)
	_ = os.WriteFile(filepath.Join(gateDir, "config.toml"), []byte(""), 0o644)

	var stdout, stderr bytes.Buffer
	err := runPromptDumpInContainer(context.Background(), promptDumpInContainerInput{
		ClaudeBin: stub, OntologyRoot: ontologyDir,
		GateConfigPath: filepath.Join(gateDir, "config.toml"),
		MindformName:   "alice", Stdout: &stdout, Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var env map[string]any
	_ = json.Unmarshal(stdout.Bytes(), &env)
	cf, _ := env["captured_from"].(map[string]any)
	if cf["identity_path"] != "" {
		t.Errorf("identity_path should be empty, got %v", cf["identity_path"])
	}
	if !bytes.Contains(stderr.Bytes(), []byte("identity not found")) {
		t.Errorf("expected identity-not-found note on stderr, got: %s", stderr.String())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/eidos/forge/ -run TestRunPromptDumpInContainer`
Expected: FAIL — `undefined: runPromptDumpInContainer`, `undefined: promptDumpInContainerInput`.

- [ ] **Step 3: Write `prompt_dump_incontainer.go`**

```go
package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/promptcapture"
	"github.com/spf13/cobra"
)

// Canonical in-container paths (must stay in sync with
// cmd/eidos/supervisor/agent_loop.go's agentLoop* constants).
const (
	promptDumpOntologyRoot   = "/eidos/ontology"
	promptDumpGateConfigPath = "/eidos/gate/config.toml"
	promptDumpClaudeDir      = "/eidos/ontology/.claude"
	promptDumpClaudeBin      = "/usr/local/bin/claude"
	promptDumpIdentityRel    = "self/identity.md"
)

// promptDumpInContainerInput is the typed input for runPromptDumpInContainer.
// All paths are explicit so tests can point at a fake filesystem.
type promptDumpInContainerInput struct {
	ClaudeBin      string
	OntologyRoot   string
	GateConfigPath string
	ClaudeDir      string // optional; empty is fine
	MindformName   string // from --mindform-name; "" if not provided
	Prompt         string
	Bare           bool
	Verbose        bool
	Stdout         io.Writer
	Stderr         io.Writer
}

// runPromptDumpInContainer reads identity.md + config.toml from the
// supplied paths, invokes promptcapture.Run, and writes the envelope
// JSON to input.Stdout.
func runPromptDumpInContainer(ctx context.Context, in promptDumpInContainerInput) error {
	if in.Stdout == nil {
		in.Stdout = os.Stdout
	}
	if in.Stderr == nil {
		in.Stderr = os.Stderr
	}

	identityPath := filepath.Join(in.OntologyRoot, promptDumpIdentityRel)
	identityBytes, err := os.ReadFile(identityPath)
	identityRel := promptDumpIdentityRel
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read identity: %w", err)
		}
		fmt.Fprintf(in.Stderr, "prompt-dump: identity not found at %s; proceeding bare\n", identityPath)
		identityBytes = nil
		identityRel = ""
	}

	var model string
	if cfg, err := config.Load(in.GateConfigPath); err == nil {
		model = cfg.MindForm.Model
	}

	identityPrompt := string(identityBytes)
	if in.Bare {
		identityPrompt = ""
		identityRel = ""
	}

	env, err := promptcapture.Run(ctx, promptcapture.Opts{
		ClaudeBin:      in.ClaudeBin,
		Cwd:            in.OntologyRoot,
		IdentityPrompt: identityPrompt,
		Model:          model,
		Prompt:         in.Prompt,
		ClaudeDir:      in.ClaudeDir,
		Verbose:        in.Verbose,
		CapturedFrom: promptcapture.CapturedFrom{
			Mindform:     in.MindformName,
			OntologyRoot: in.OntologyRoot,
			Model:        model,
			IdentityPath: identityRel,
			Bare:         in.Bare,
		},
		NoPOSTAfter: 10 * time.Second,
	})
	if err != nil {
		return err
	}

	enc := json.NewEncoder(in.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(env)
}

func newPromptDumpInContainerCmd() *cobra.Command {
	var (
		prompt       string
		bare         bool
		mindformName string
		verbose      bool
	)
	cmd := &cobra.Command{
		Use:    "prompt-dump",
		Short:  "(in-container) Capture this mind-form's /v1/messages envelope",
		Hidden: true, // operators discover the host wrapper, not the worker
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPromptDumpInContainer(cmd.Context(), promptDumpInContainerInput{
				ClaudeBin:      promptDumpClaudeBin,
				OntologyRoot:   promptDumpOntologyRoot,
				GateConfigPath: promptDumpGateConfigPath,
				ClaudeDir:      promptDumpClaudeDir,
				MindformName:   mindformName,
				Prompt:         prompt,
				Bare:           bare,
				Verbose:        verbose,
				Stdout:         cmd.OutOrStdout(),
				Stderr:         cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "ping", "stub prompt sent to claude")
	cmd.Flags().BoolVar(&bare, "bare", false, "omit --append-system-prompt")
	cmd.Flags().StringVar(&mindformName, "mindform-name", "",
		"recorded in captured_from.mindform; host wrapper supplies this")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "log proxy traffic + claude stderr")
	return cmd
}
```

- [ ] **Step 4: Register the in-container subcommand**

Open `cmd/eidos/forge/incontainer.go`. In `registerInContainer`, add `newPromptDumpInContainerCmd()` to the list:

```go
func registerInContainer(root *cobra.Command) {
	root.AddCommand(
		newWhoamiCmd(),
		newInboxCmd(),
		newSendCmd(),
		newMemoryCmd(),
		newOntologyStatusCmd(),
		newWakeInContainerCmd(),
		newInitVolumeCmd(),
		newPlanInContainerCmd(),
		newDreamCmd(),
		newStatusDetailCmd(),
		newRuntimeStateCmd(),
		newAgentStateCmd(),
		newTranscriptListCmd(),
		newTranscriptTailCmd(),
		newPromptDumpInContainerCmd(), // NEW
	)
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./cmd/eidos/forge/ -run TestRunPromptDumpInContainer -v`
Expected: all three sub-tests PASS.

- [ ] **Step 6: Verify the binary builds**

Run: `go build ./cmd/eidos`
Expected: exits 0.

- [ ] **Step 7: Commit**

```bash
git add cmd/eidos/forge/prompt_dump_incontainer.go \
        cmd/eidos/forge/prompt_dump_incontainer_test.go \
        cmd/eidos/forge/incontainer.go
git commit -m "feat(forge): add in-container prompt-dump worker"
```

---

## Task 8: Add host-side `forge prompt-dump <name>` wrapper (TDD)

Register the host subcommand. Use the existing `forgectl.Client` fake pattern (see `cmd/eidos/forge/status_test.go`) to verify argv, error paths, and `-o` dual-write.

**Files:**
- Create: `cmd/eidos/forge/prompt_dump.go`
- Create: `cmd/eidos/forge/prompt_dump_test.go`
- Modify: `cmd/eidos/forge/incontainer.go` (register the host subcommand)

- [ ] **Step 1: Find an existing forgectl fake to copy the pattern from**

Open `cmd/eidos/forge/status_test.go` and skim — it should define a fake `forgectl.Client` (or use one in a sibling test helper). Note the type name and how it stubs `ContainerInspectState` + `ContainerExec`. The new test will reuse the same pattern.

- [ ] **Step 2: Write the failing test (`prompt_dump_test.go`)**

```go
package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// fakePromptDumpClient is a minimal forgectl.Client double for the host
// wrapper test. It returns canned values for inspect/exec.
//
// (If a shared fake already exists in this package — e.g. from
// status_test.go — use that instead of duplicating here.)
type fakePromptDumpClient struct {
	state    string
	stateErr error
	execArgs []string
	execOut  []byte
	execExit int
	execErr  error
}

func (f *fakePromptDumpClient) ContainerInspectState(ctx context.Context, name string) (string, error) {
	return f.state, f.stateErr
}
func (f *fakePromptDumpClient) ContainerExec(ctx context.Context, name string, cmd []string) (forgectl.ExecResult, error) {
	f.execArgs = cmd
	return forgectl.ExecResult{Stdout: f.execOut, ExitCode: f.execExit}, f.execErr
}

// Stub the remaining forgectl.Client methods. Only used to satisfy the
// interface; the prompt-dump wrapper does not call them.
func (f *fakePromptDumpClient) ContainerExists(context.Context, string) (bool, error)                 { return true, nil }
func (f *fakePromptDumpClient) ContainerCreate(context.Context, forgectl.CreateOpts) error            { return nil }
func (f *fakePromptDumpClient) ContainerStart(context.Context, string) error                          { return nil }
func (f *fakePromptDumpClient) ContainerStop(context.Context, string, int) error                      { return nil }
func (f *fakePromptDumpClient) ContainerRemove(context.Context, string) error                         { return nil }
func (f *fakePromptDumpClient) ContainerLogs(context.Context, string, bool, io.Writer) error          { return nil }
func (f *fakePromptDumpClient) CopyFromContainer(context.Context, string, string, io.Writer) error    { return nil }
func (f *fakePromptDumpClient) ImageExists(context.Context, string) (bool, error)                     { return true, nil }
func (f *fakePromptDumpClient) ImagePull(context.Context, string, io.Writer) error                    { return nil }
func (f *fakePromptDumpClient) RunInit(context.Context, forgectl.RunInitOpts) (forgectl.RunInitResult, error) {
	return forgectl.RunInitResult{}, nil
}

// (If the real forgectl.Client interface has additional methods not stubbed
// above, copy their signatures from internal/forgectl/forgectl.go and add
// no-op implementations here. Compiler errors will list any missing methods.)

func TestPromptDumpHost_ContainerNotRunning(t *testing.T) {
	c := &fakePromptDumpClient{state: "exited"}
	var out, errOut bytes.Buffer
	err := runPromptDumpHost(context.Background(), promptDumpHostInput{
		Client: c, Name: "alice", Stdout: &out, Stderr: &errOut,
	})
	if err == nil {
		t.Fatal("expected error when container not running")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("error=%v", err)
	}
}

func TestPromptDumpHost_ExecArgsIncludeMindformName(t *testing.T) {
	c := &fakePromptDumpClient{
		state:   "running",
		execOut: []byte(`{"captured_at":"x","captured_from":{"mindform":"alice"}}` + "\n"),
	}
	var out bytes.Buffer
	err := runPromptDumpHost(context.Background(), promptDumpHostInput{
		Client: c, Name: "alice", Prompt: "ping", Stdout: &out, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []string{"eidos", "forge", "prompt-dump", "--mindform-name", "alice", "--prompt", "ping"}
	for _, w := range want {
		found := false
		for _, a := range c.execArgs {
			if a == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("execArgs missing %q: %v", w, c.execArgs)
		}
	}
	if !strings.Contains(out.String(), `"mindform":"alice"`) {
		t.Errorf("stdout did not contain envelope: %s", out.String())
	}
}

func TestPromptDumpHost_BarePassedThrough(t *testing.T) {
	c := &fakePromptDumpClient{
		state:   "running",
		execOut: []byte(`{}` + "\n"),
	}
	err := runPromptDumpHost(context.Background(), promptDumpHostInput{
		Client: c, Name: "alice", Bare: true, Stdout: &bytes.Buffer{}, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	found := false
	for _, a := range c.execArgs {
		if a == "--bare" {
			found = true
		}
	}
	if !found {
		t.Errorf("--bare not in execArgs: %v", c.execArgs)
	}
}

func TestPromptDumpHost_OutPathDualWrite(t *testing.T) {
	envJSON, _ := json.Marshal(map[string]any{
		"captured_at": "x",
		"request":     map[string]any{"system": []any{}},
	})
	c := &fakePromptDumpClient{
		state:   "running",
		execOut: append(envJSON, '\n'),
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "snap")
	err := runPromptDumpHost(context.Background(), promptDumpHostInput{
		Client: c, Name: "alice", OutPath: base,
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(base + ".json"); err != nil {
		t.Errorf("json missing: %v", err)
	}
	if _, err := os.Stat(base + ".md"); err != nil {
		t.Errorf("md missing: %v", err)
	}
}
```

(Add `"io"` import at the top of the file.)

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./cmd/eidos/forge/ -run TestPromptDumpHost`
Expected: FAIL — `undefined: runPromptDumpHost`, `undefined: promptDumpHostInput`.

- [ ] **Step 4: Write `prompt_dump.go`**

```go
package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/promptcapture"
	"github.com/spf13/cobra"
)

type promptDumpHostInput struct {
	Client  forgectl.Client
	Name    string
	Prompt  string
	OutPath string
	Bare    bool
	Verbose bool
	Stdout  io.Writer
	Stderr  io.Writer
}

func runPromptDumpHost(ctx context.Context, in promptDumpHostInput) error {
	if in.Stdout == nil {
		in.Stdout = os.Stdout
	}
	if in.Stderr == nil {
		in.Stderr = os.Stderr
	}
	if err := forgectl.ValidateName(in.Name); err != nil {
		return err
	}
	cont := forgectl.ContainerName(in.Name)
	state, err := in.Client.ContainerInspectState(ctx, cont)
	if err != nil {
		return err
	}
	if state == "absent" {
		return fmt.Errorf("mind-form %q not found", in.Name)
	}
	if state != "running" {
		return fmt.Errorf("mind-form %q not running (state: %s)", in.Name, state)
	}

	args := []string{
		"eidos", "forge", "prompt-dump",
		"--mindform-name", in.Name,
	}
	if in.Prompt != "" {
		args = append(args, "--prompt", in.Prompt)
	}
	if in.Bare {
		args = append(args, "--bare")
	}
	if in.Verbose {
		args = append(args, "-v")
	}

	res, err := in.Client.ContainerExec(ctx, cont, args)
	if err != nil {
		return fmt.Errorf("docker exec: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("in-container prompt-dump exited %d: %s",
			res.ExitCode, string(res.Stderr))
	}

	if in.OutPath == "" {
		// Stream the envelope JSON to host stdout verbatim — no
		// re-serialization needed.
		_, err := in.Stdout.Write(res.Stdout)
		return err
	}

	// -o set: parse and dual-write on the host.
	var env map[string]any
	if err := json.Unmarshal(res.Stdout, &env); err != nil {
		return fmt.Errorf("parse envelope from container: %w (stdout=%q)", err, string(res.Stdout))
	}
	wrote, err := promptcapture.WriteOutputs(env, in.OutPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(in.Stderr, "prompt-dump: wrote %s\n", joinPaths(wrote))
	return nil
}

func joinPaths(ps []string) string {
	out := ""
	for i, p := range ps {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

func newPromptDumpHostCmd() *cobra.Command {
	var (
		prompt  string
		bare    bool
		verbose bool
		outPath string
	)
	cmd := &cobra.Command{
		Use:   "prompt-dump <name>",
		Short: "Capture the /v1/messages request envelope this mind-form would send right now",
		Long: `Capture the /v1/messages request envelope this mind-form's claude would
send right now: the default system prompt, tool catalogue, the layered
identity.md, the ontology's CLAUDE.md, the configured model, and the
first-message ambient context block.

This is a one-shot snapshot of a fresh session — it does not affect the
running agent-loop and does not include the rolling conversation history
of in-flight turns. For the response side (assistant thinking + tool
calls), use ` + "`eidos forge watch`" + `.

The capture runs inside the mind-form's container, using the mind-form's
own identity.md, config.toml, ontology CLAUDE.md, and claude binary.

Examples:
  eidos forge prompt-dump alice                # JSON envelope to stdout
  eidos forge prompt-dump alice -o snap        # writes snap.json + snap.md
  eidos forge prompt-dump alice -o snap.md     # Markdown only
  eidos forge prompt-dump alice --bare         # skip --append-system-prompt
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := forgectl.New()
			if err != nil {
				return err
			}
			return runPromptDumpHost(cmd.Context(), promptDumpHostInput{
				Client:  c,
				Name:    args[0],
				Prompt:  prompt,
				OutPath: outPath,
				Bare:    bare,
				Verbose: verbose,
				Stdout:  cmd.OutOrStdout(),
				Stderr:  cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "ping", "stub prompt sent to claude")
	cmd.Flags().StringVarP(&outPath, "output", "o", "",
		"write envelope to this path; .json/.md selects format, else both")
	cmd.Flags().BoolVar(&bare, "bare", false, "omit --append-system-prompt")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false,
		"surface in-container stderr (proxy + claude) to host stderr")
	return cmd
}
```

Add `"os"` to the imports.

- [ ] **Step 5: Register the host subcommand**

In `cmd/eidos/forge/incontainer.go`, append to `registerHost`:

```go
func registerHost(root *cobra.Command) {
	root.AddCommand(
		newCreateCmd(),
		newStartCmd(),
		newStopCmd(),
		newStatusCmd(),
		newListCmd(),
		newLogsCmd(),
		newExecCmd(),
		newWakeHostCmd(),
		newLoginCmd(),
		newOntologyCmd(),
		newPurgeCmd(),
		newConfigCmd(),
		newPlanHostCmd(),
		newWatchCmd(),
		newPromptDumpHostCmd(), // NEW
	)
}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./cmd/eidos/forge/ -run TestPromptDumpHost -v`
Expected: all four sub-tests PASS. If the compiler complains that `fakePromptDumpClient` doesn't satisfy `forgectl.Client`, add stub methods for the missing signatures listed in the compiler output (no-op return values are fine; the wrapper only calls `ContainerInspectState` and `ContainerExec`).

- [ ] **Step 7: Build the binary**

Run: `go build ./cmd/eidos && ./eidos forge prompt-dump --help`
Expected: builds; help text shows the new command.

- [ ] **Step 8: Commit**

```bash
git add cmd/eidos/forge/prompt_dump.go \
        cmd/eidos/forge/prompt_dump_test.go \
        cmd/eidos/forge/incontainer.go
git commit -m "feat(forge): add host-side prompt-dump wrapper"
```

---

## Task 9: Lint, full test pass, and full repo build

Verify nothing else in the workspace broke.

- [ ] **Step 1: Format + vet**

Run from repo root:
```
gofmt -l .
go vet ./...
```
Expected: `gofmt -l .` outputs nothing; `go vet ./...` exits 0.

- [ ] **Step 2: Full unit test pass (workspace)**

Run: `go test ./...`
Expected: all packages PASS. Address any regressions before continuing.

- [ ] **Step 3: utils/promptdump tests pass under GOWORK=off**

Run:
```
cd utils/promptdump && GOWORK=off go test -short ./...
```
Expected: PASS.

- [ ] **Step 4: Verify the eidos binary still builds for the host**

Run from repo root: `go build -o /tmp/eidos-test ./cmd/eidos && /tmp/eidos-test forge prompt-dump --help && rm /tmp/eidos-test`
Expected: builds; help renders.

- [ ] **Step 5: Commit (if any formatting/vet fixes were needed)**

```bash
git add -A
git diff --cached --quiet || git commit -m "chore: gofmt + vet fixes"
```

(If nothing to commit, this is a no-op.)

---

## Task 10: Update documentation

Update README, utils CLAUDE.md, and project CLAUDE.md / SPEC.md per the spec.

**Files:**
- Modify: `README.md`
- Modify: `utils/promptdump/README.md`
- Modify: `utils/CLAUDE.md` (and the linked `utils/AGENTS.md`)
- Modify: `CLAUDE.md` (and the linked `AGENTS.md`)

- [ ] **Step 1: Update `README.md`**

Find the section that lists inspection / observability commands (look for `forge watch` mentioned there, or under "Observability" / "Inspection"). Add a sibling bullet:

```markdown
- `eidos forge prompt-dump <name>` — capture the verbatim `/v1/messages`
  request envelope this mind-form's claude would send right now (default
  system prompt, tool catalogue, identity.md, ontology `CLAUDE.md`,
  model, first-message ambient context). One-shot; safe to run against
  a running mind-form without affecting the agent-loop.
```

If no such section exists, locate the existing `forge watch` mention and add the bullet next to it.

- [ ] **Step 2: Rewrite the opening of `utils/promptdump/README.md`**

Replace the existing first paragraph with:

```markdown
# promptdump (host-only dev shim)

This is the **dev-only** host-side capture utility. It runs `claude` from
your host's PATH against an arbitrary prompt / flag combination and dumps
the verbatim `/v1/messages` request body so you can study Claude Code's
default system prompt outside of any mind-form context.

For capturing a real mind-form's envelope — using the mind-form's own
`identity.md`, `config.toml`, ontology `CLAUDE.md`, and in-container
`claude` binary — use `eidos forge prompt-dump <name>` instead.

The capture core is in `internal/promptcapture`; this binary is a thin
wrapper that stays outside `go.work` (see `utils/CLAUDE.md`). All `go`
invocations require `GOWORK=off`.
```

Leave the rest of the file (flag table, jq examples, privacy caveat,
testing section) intact. The `GOWORK=off` paragraph below the opening
already exists — confirm it does, otherwise keep the existing copy.

- [ ] **Step 3: Update `utils/CLAUDE.md` (touches AGENTS.md via the softlink)**

Open `utils/AGENTS.md` (the real file; `utils/CLAUDE.md` is a softlink to it — verify with `ls -l utils/CLAUDE.md`). Add a short paragraph in the "Current contents" section:

```markdown
- `promptdump/` — host-only dev shim over `internal/promptcapture`.
  Captures Claude Code's `/v1/messages` request body for arbitrary
  flag combinations. For mind-form-bound captures, use
  `eidos forge prompt-dump <name>` instead. Stays outside `go.work`
  via a `replace github.com/LucianoXu/eidopsyche => ../..` directive
  in its `go.mod`.
```

If the existing bullet is more verbose, edit it in place rather than duplicating.

- [ ] **Step 4: Update root `CLAUDE.md` (touches `AGENTS.md` via softlink)**

Open `AGENTS.md` (the real file). Find the inspection / observability discussion (or `forge watch` references). Add one or two sentences explaining the new sibling:

```markdown
The introspection family pairs response-side and request-side views:

- `eidos forge watch <name>` — streams the response side (assistant
  thinking, tool calls, tool results) from the running agent-loop.
- `eidos forge prompt-dump <name>` — one-shot capture of the request
  side (default system prompt, tool catalogue, identity.md, ontology
  `CLAUDE.md`, model) for a fresh session. Does not affect the running
  agent-loop.
```

If no good slot exists, append the two-bullet block near the top of the document's "what eidos ships" section.

- [ ] **Step 5: Verify the softlinks**

Run: `ls -l CLAUDE.md utils/CLAUDE.md`
Expected: both are symlinks to their `AGENTS.md` neighbour, per project convention. If broken, re-create with `ln -sf AGENTS.md CLAUDE.md` and `ln -sf AGENTS.md utils/CLAUDE.md`.

- [ ] **Step 6: Commit**

```bash
git add README.md utils/promptdump/README.md utils/AGENTS.md AGENTS.md
git commit -m "docs: document forge prompt-dump and the promptcapture promotion"
```

---

## Task 11: Open the PR

- [ ] **Step 1: Push the branch + open the PR**

Run:
```
git push -u origin <branch-name>
gh pr create --title "feat(forge): prompt-dump — per-mindform request envelope capture" --body "$(cat <<'EOF'
## Summary
- New `eidos forge prompt-dump <name>` (host) + in-container worker captures the verbatim `/v1/messages` request a mind-form would send right now (default system prompt, tool catalogue, identity.md, ontology `CLAUDE.md`, model). Paralels `forge watch`'s response-side view; zero impact on the running agent-loop.
- Promotes the existing `utils/promptdump` core into `internal/promptcapture`; the utils/ binary stays as a thin host-only shim (kept outside `go.work` via a `replace` directive).
- Spec: `docs/superpowers/specs/2026-05-12-forge-prompt-dump-design.md`.

## Test plan
- [ ] `go test ./...` passes
- [ ] `cd utils/promptdump && GOWORK=off go test -short ./...` passes
- [ ] `go build ./cmd/eidos` succeeds; `./eidos forge prompt-dump --help` shows the new command
- [ ] Smoke against a real mind-form: `eidos forge prompt-dump <name>` returns a parseable JSON envelope with `captured_from.mindform == <name>`
- [ ] `eidos forge prompt-dump <name> -o snap` writes both `snap.json` and `snap.md`
- [ ] `eidos forge prompt-dump <name> --bare` produces an envelope whose `claude_args` does **not** contain `--append-system-prompt`

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

(The branch name will be set during execution by the worktree skill; the executing agent will fill it in.)

- [ ] **Step 2: Watch CI**

Run: `gh pr checks --watch`
Expected: all checks PASS. If a check fails, fix the issue and push a new commit (do not amend).

---

## Self-review (spec coverage)

Sanity check against `docs/superpowers/specs/2026-05-12-forge-prompt-dump-design.md`:

- **Source decision (S1):** Implemented by Task 5 (Run spawns a fresh `claude` with a new session via `-p`, no `--resume`, no agent-loop interaction).
- **Host vs in-container (in-container):** Tasks 7 + 8 wire the worker + wrapper exactly that way.
- **internal/promptcapture layout (proxy.go, envelope.go, capture.go):** Tasks 2 + 3 + 4 + 5 lay them down.
- **utils/promptdump becomes a thin wrapper:** Task 6.
- **`captured_from` envelope field + Markdown header:** Task 3 (struct + render); Task 7 (populated by the worker).
- **`-p`, `--bare`, `-o`, `-v` flags:** Tasks 7 + 8.
- **CLAUDE_DIR=/eidos/ontology/.claude in spawn env:** Task 5 (`buildSpawnEnv`) + Task 7 (`promptDumpClaudeDir` const passed through).
- **Capture timeout default 10s:** Task 5 (`NoPOSTAfter` default), Task 7 (passes 10s explicitly).
- **Edge case: identity.md missing → empty IdentityPrompt + stderr note:** Task 7 step 3, covered by `TestRunPromptDumpInContainer_MissingIdentityProceedsWithEmpty`.
- **Edge case: container not running → clean host error:** Task 8 step 2, covered by `TestPromptDumpHost_ContainerNotRunning`.
- **Edge case: concurrent invocations safe:** Implicit — each capture binds a fresh random port and spawns a fresh process; no shared state. No explicit test (cost vs benefit unfavourable).
- **Edge case: capture timeout returns clear error:** Task 5 (`TestRunReturnsTimeoutWhenStubPostsNothing`).
- **`--mindform-name` flag on the worker, supplied by host:** Task 7 (flag) + Task 8 (argv).
- **Docs (README, utils/CLAUDE.md, root CLAUDE.md, utils/promptdump/README.md):** Task 10.
- **Smoke test against real claude:** Out of scope for unit tests (the spec calls it "gated by `testing.Short()`" — the existing `utils/promptdump` smoke already covers a real-claude path and is preserved by Task 6; we explicitly delete only the *internals* tests that moved into `internal/promptcapture`).

No placeholders. Type names consistent across tasks (`Opts`, `CapturedFrom`, `EnvelopeMeta`, `Run`, `BuildEnvelopeMap`, `RenderMarkdown`, `WriteOutputs`, `promptDumpInContainerInput`, `promptDumpHostInput`, `runPromptDumpInContainer`, `runPromptDumpHost`).
