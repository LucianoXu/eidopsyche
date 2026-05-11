# `utils/promptdump` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `utils/promptdump`, a small Go binary that captures the Anthropic Messages API request body Claude Code sends, so we can study the verbatim default system prompt under any combination of `claude` flags (`--model`, `--effort`, `--append-system-prompt`, `--bare`, …).

**Architecture:** A standalone Go binary in a new top-level `utils/` directory (its own module, **not** in `go.work`). It starts a local HTTP server on a random localhost port, spawns `claude -p <prompt>` with `ANTHROPIC_BASE_URL` pointing at that server and `ANTHROPIC_API_KEY=sk-dummy-promptdump`, captures the first POST to `/v1/messages`, returns a minimal valid SSE 200 so claude exits cleanly, and writes a JSON envelope `{captured_at, claude_version, claude_args, host, request}` to stdout or `-o <path>`.

**Tech Stack:** Go 1.25 stdlib only — `net/http`, `os/exec`, `encoding/json`, `flag`, `context`. No third-party deps.

**Spec:** `docs/superpowers/specs/2026-05-11-utils-promptdump-design.md`

> **Note on `go` invocations in this plan:** Because `utils/promptdump`
> is intentionally outside the root `go.work`, **every `go build` /
> `go test` / `go run` command below assumes `GOWORK=off` in the
> environment.** Without it, Go finds the parent workspace and fails
> with "main module ... does not contain package ...". From inside
> `utils/promptdump/`: `GOWORK=off go test ./...`. From the repo root:
> `GOWORK=off go -C utils/promptdump test ./...`. The bare forms are
> kept in the steps for readability.

---

## File Structure

| Path | Purpose | Touched by |
|---|---|---|
| `utils/README.md` | One-paragraph policy: dev-only utilities, independent modules, not in `go.work`, not shipped. | Task 2 |
| `utils/promptdump/go.mod` | Standalone module `github.com/LucianoXu/eidopsyche/utils/promptdump`, `go 1.25`. | Task 2 |
| `utils/promptdump/main.go` | All logic in one file: flag parsing, capture server, claude spawn, envelope construction. | Tasks 2–11 |
| `utils/promptdump/main_test.go` | Five unit tests covering the capture-server contract; no real-claude invocation. | Tasks 3–7, 9–10 |
| `utils/promptdump/README.md` | How to run, example commands, sample output, privacy caveat. | Task 12 |
| `CLAUDE.md` | Add `utils/` to the project structure list + a sentence on policy. | Task 13 |
| `docs/superpowers/specs/2026-05-11-utils-promptdump-design.md` | Spec, already written in main repo working tree (uncommitted at plan time). Carried into the feature branch via Task 1. | Task 1 |
| `docs/superpowers/plans/2026-05-11-utils-promptdump.md` | This plan. Same — written in main repo, carried via Task 1. | Task 1 |

---

## Task 1: Set up the worktree and carry over spec + plan

**Files:**
- Create: `.claude/worktrees/feat-utils-promptdump/` (worktree dir)
- Move: `docs/superpowers/specs/2026-05-11-utils-promptdump-design.md` (already exists in main repo, uncommitted)
- Move: `docs/superpowers/plans/2026-05-11-utils-promptdump.md` (this file, in main repo, uncommitted)

- [ ] **Step 1: Create the worktree branched off `main`**

```bash
git -C /data/eidopsyche worktree add -b feat/utils-promptdump /data/eidopsyche/.claude/worktrees/feat-utils-promptdump main
```

Expected: "Preparing worktree (new branch 'feat/utils-promptdump')". Verify with:

```bash
git -C /data/eidopsyche worktree list
```

- [ ] **Step 2: Carry the uncommitted spec and plan into the worktree**

The spec and plan currently live in `/data/eidopsyche/docs/superpowers/{specs,plans}/...` as uncommitted files in the main repo. The worktree was created from `main`'s HEAD, so those files don't exist there yet. Copy them in:

```bash
cp /data/eidopsyche/docs/superpowers/specs/2026-05-11-utils-promptdump-design.md \
   /data/eidopsyche/.claude/worktrees/feat-utils-promptdump/docs/superpowers/specs/

cp /data/eidopsyche/docs/superpowers/plans/2026-05-11-utils-promptdump.md \
   /data/eidopsyche/.claude/worktrees/feat-utils-promptdump/docs/superpowers/plans/
```

Then **delete** the uncommitted copies from the main repo working tree so they don't double-count:

```bash
rm /data/eidopsyche/docs/superpowers/specs/2026-05-11-utils-promptdump-design.md
rm /data/eidopsyche/docs/superpowers/plans/2026-05-11-utils-promptdump.md
```

- [ ] **Step 3: Commit spec + plan as the first commit on the branch**

```bash
cd /data/eidopsyche/.claude/worktrees/feat-utils-promptdump
git add docs/superpowers/specs/2026-05-11-utils-promptdump-design.md \
        docs/superpowers/plans/2026-05-11-utils-promptdump.md
git commit -m "$(cat <<'EOF'
docs(promptdump): design spec and implementation plan

Spec: capture Claude Code's Anthropic API request body via a local HTTP
proxy with ANTHROPIC_BASE_URL override. Plan: 15 tasks landing under a
new top-level utils/ directory as an independent Go module.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

Verify the worktree's `git status` is clean afterwards.

**All subsequent tasks run inside `/data/eidopsyche/.claude/worktrees/feat-utils-promptdump`.**

---

## Task 2: Scaffold `utils/` with an empty buildable promptdump

**Files:**
- Create: `utils/README.md`
- Create: `utils/promptdump/go.mod`
- Create: `utils/promptdump/main.go` (placeholder — prints "promptdump: not yet implemented" and exits 0)

- [ ] **Step 1: Write `utils/README.md`**

```markdown
# `utils/` — dev-only utilities

This directory holds standalone Go utilities used during eidopsyche
development. They are **not** part of the shipped `eidos` binary.

Policy:

- Each utility is its own Go module with its own `go.mod`.
- `utils/` is **not** included in the root `go.work` file. The root
  workspace's `go build ./...` / `go test ./...` do not touch utilities,
  and adding a dependency here does not bloat the `eidos` import graph.
- Utilities are invoked explicitly, e.g. `cd utils/promptdump && go run .`
  or `go run ./utils/promptdump` from inside the utility's own module.
- Utilities are not built in CI by default and are not released. Authors
  add their own CI job if they want one.
- Utilities may be deleted at any time — there is no stability contract.
```

- [ ] **Step 2: Write `utils/promptdump/go.mod`**

```
module github.com/LucianoXu/eidopsyche/utils/promptdump

go 1.25
```

- [ ] **Step 3: Write `utils/promptdump/main.go` placeholder**

```go
// Package main implements promptdump, a dev utility that captures the
// Anthropic Messages API request body Claude Code sends, so the verbatim
// default system prompt can be inspected under any combination of claude
// CLI flags. See docs/superpowers/specs/2026-05-11-utils-promptdump-design.md.
package main

import "fmt"

func main() {
	fmt.Println("promptdump: not yet implemented")
}
```

- [ ] **Step 4: Verify it builds and runs**

```bash
cd utils/promptdump
go build ./...
go run .
```

Expected: `promptdump: not yet implemented` on stdout, exit 0.

- [ ] **Step 5: Run `gofmt` and `go vet`**

```bash
gofmt -l .
go vet ./...
```

Expected: no output from either.

- [ ] **Step 6: Commit**

```bash
cd /data/eidopsyche/.claude/worktrees/feat-utils-promptdump
git add utils/README.md utils/promptdump/
git commit -m "$(cat <<'EOF'
feat(utils): scaffold utils/ with empty promptdump module

Independent Go module under a new top-level utils/ directory. Not in
go.work so the eidos build/test surface is untouched. Placeholder main
prints a "not yet implemented" line; subsequent commits flesh it out.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Capture server happy path (TDD)

We build the HTTP capture server inside `main.go` as a `captureServer` type. Its sole job: accept one POST to `/v1/messages*`, store the body, respond with a minimal valid SSE 200.

**Files:**
- Modify: `utils/promptdump/main.go`
- Create: `utils/promptdump/main_test.go`

- [ ] **Step 1: Write the failing test**

Create `utils/promptdump/main_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestCaptureServer_HappyPath: a POST to /v1/messages?beta=true with a
// known JSON body returns 200 + valid SSE, and the body is captured
// verbatim into the server's captured field.
func TestCaptureServer_HappyPath(t *testing.T) {
	srv := newCaptureServer()
	httpSrv := srv.start(t)
	defer httpSrv.Close()

	want := map[string]any{
		"model":    "claude-sonnet-4-7",
		"system":   "you are a fox spirit",
		"messages": []any{map[string]any{"role": "user", "content": "ping"}},
	}
	wantBody, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp, err := http.Post(httpSrv.URL+"/v1/messages?beta=true",
		"application/json", bytes.NewReader(wantBody))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream prefix", ct)
	}
	respBody, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(respBody), "message_stop") {
		t.Errorf("SSE body missing message_stop terminator:\n%s", respBody)
	}

	// Wait briefly for the server's capture-done channel to fire.
	select {
	case <-srv.done:
	case <-time.After(time.Second):
		t.Fatal("capture done not signaled within 1s")
	}

	if string(srv.captured) != string(wantBody) {
		t.Errorf("captured body mismatch.\nwant: %s\ngot:  %s", wantBody, srv.captured)
	}

	_ = context.TODO()
}
```

- [ ] **Step 2: Run the test to confirm it fails**

```bash
cd utils/promptdump
go test ./... -run TestCaptureServer_HappyPath
```

Expected: compile error / `newCaptureServer` undefined.

- [ ] **Step 3: Implement the minimal capture server**

Replace `utils/promptdump/main.go` with:

```go
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
	"strings"
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
// the *httptest.Server so the test can read its URL. The non-test entry
// point lives in main() and uses a real http.Server bound to 127.0.0.1
// on a random port.
func (s *captureServer) start(_ testing.TB) *httptest.Server {
	return httptest.NewServer(s.handler())
}

func main() {
	// Wired up in a later task. For now main() just announces itself so
	// `go run .` continues to do something visible.
	fmt.Println("promptdump: not yet implemented")
	_ = strings.TrimSpace
}
```

Note: `start` takes a `testing.TB` so production code doesn't accidentally call it — only tests can. Production wiring (real `http.Server` on a random port) lands in Task 8.

- [ ] **Step 4: Run the test to verify it passes**

```bash
cd utils/promptdump
go test ./... -run TestCaptureServer_HappyPath -v
```

Expected: PASS.

- [ ] **Step 5: gofmt + vet**

```bash
gofmt -l .
go vet ./...
```

Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add utils/promptdump/main.go utils/promptdump/main_test.go
git commit -m "$(cat <<'EOF'
feat(promptdump): capture server happy path

Adds captureServer with /v1/messages handler that records the first POST
body and responds with a minimal valid SSE stream. Catch-all handler
returns 200 {} so ancillary claude startup probes don't 404-cascade.
Test covers a known JSON body POSTed to /v1/messages?beta=true.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Only `/v1/messages` triggers capture (TDD)

Verify that the catch-all handler does **not** populate `captured`.

**Files:**
- Modify: `utils/promptdump/main_test.go`

- [ ] **Step 1: Add the failing test**

Append to `main_test.go`:

```go
// TestCaptureServer_OnlyMessagesCaptures: POSTs to non-/v1/messages
// paths must not populate srv.captured and must not signal srv.done.
func TestCaptureServer_OnlyMessagesCaptures(t *testing.T) {
	srv := newCaptureServer()
	httpSrv := srv.start(t)
	defer httpSrv.Close()

	for _, path := range []string{"/v1/models", "/v1/mcp_servers", "/v1/whatever"} {
		resp, err := http.Post(httpSrv.URL+path, "application/json",
			strings.NewReader(`{"probe":true}`))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("POST %s status = %d, want 200", path, resp.StatusCode)
		}
	}

	if srv.captured != nil {
		t.Errorf("captured populated by non-messages traffic: %s", srv.captured)
	}
	select {
	case <-srv.done:
		t.Fatal("done channel signaled by non-messages traffic")
	default:
	}
}
```

- [ ] **Step 2: Run — expect PASS already**

```bash
go test ./... -run TestCaptureServer_OnlyMessagesCaptures -v
```

Expected: PASS (the implementation from Task 3 already routes correctly; this test guards against future regressions).

- [ ] **Step 3: Commit**

```bash
git add utils/promptdump/main_test.go
git commit -m "$(cat <<'EOF'
test(promptdump): non-messages traffic must not trigger capture

Regression guard for the catch-all handler: POSTs to /v1/models,
/v1/mcp_servers, etc. return 200 but leave srv.captured/srv.done
untouched.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Body-too-large guard (TDD)

The probe revealed claude POSTs ~200 KB. Cap at 16 MB to defend against runaway memory under future claude versions.

**Files:**
- Modify: `utils/promptdump/main.go`
- Modify: `utils/promptdump/main_test.go`

- [ ] **Step 1: Add the failing test**

Append to `main_test.go`:

```go
// TestCaptureServer_BodyTooLarge: a POST with Content-Length above the
// 16 MB cap must return 413 and leave srv.captured nil.
func TestCaptureServer_BodyTooLarge(t *testing.T) {
	srv := newCaptureServer()
	httpSrv := srv.start(t)
	defer httpSrv.Close()

	big := bytes.Repeat([]byte("x"), maxBodyBytes+1)
	resp, err := http.Post(httpSrv.URL+"/v1/messages",
		"application/octet-stream", bytes.NewReader(big))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
	if srv.captured != nil {
		t.Errorf("captured populated despite oversize body")
	}
}
```

- [ ] **Step 2: Run — expect FAIL (undefined identifier)**

```bash
go test ./... -run TestCaptureServer_BodyTooLarge
```

Expected: compile error, `maxBodyBytes` undefined.

- [ ] **Step 3: Add the cap to main.go**

Add a package-level constant near the top of `main.go`:

```go
// maxBodyBytes caps the request body the capture handler will accept.
// Claude Code's observed payload is ~200 KB (probe 2026-05-11). 16 MB
// is a generous ceiling that still defends against runaway memory if
// a future version starts shipping huge payloads.
const maxBodyBytes = 16 << 20
```

Then change `handleMessages` to enforce it. Replace the `body, err := io.ReadAll(r.Body)` lines with:

```go
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if len(body) > maxBodyBytes {
		http.Error(w, "request body exceeds 16 MB cap", http.StatusRequestEntityTooLarge)
		return
	}
```

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./... -run TestCaptureServer -v
```

Expected: all three TestCaptureServer_* pass.

- [ ] **Step 5: Commit**

```bash
git add utils/promptdump/main.go utils/promptdump/main_test.go
git commit -m "$(cat <<'EOF'
feat(promptdump): cap captured body at 16 MB

LimitReader-based guard; oversize bodies return 413 and do not populate
captured. Defensive against future claude versions shipping larger
payloads; today's observed size is ~200 KB.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Envelope construction (TDD)

The envelope wraps the captured body with reproducibility metadata. Define `buildEnvelope` so it's testable independent of HTTP and exec.

**Files:**
- Modify: `utils/promptdump/main.go`
- Modify: `utils/promptdump/main_test.go`

- [ ] **Step 1: Write the failing test**

Append to `main_test.go`:

```go
// TestBuildEnvelope: wraps a captured body with metadata and returns a
// well-formed JSON document. captured_at must be RFC3339; request must
// be the parsed body, not a string blob.
func TestBuildEnvelope(t *testing.T) {
	captured := []byte(`{"model":"claude-sonnet-4-7","system":"foo"}`)
	meta := envelopeMeta{
		ClaudeVersion: "2.1.138",
		ClaudePath:    "/usr/bin/claude",
		ClaudeArgs:    []string{"--model", "sonnet", "-p", "ping"},
		Host:          hostInfo{Platform: "linux", CWD: "/data/eidopsyche"},
		CapturedAt:    time.Date(2026, 5, 11, 17, 23, 45, 0, time.UTC),
	}
	out, err := buildEnvelope(meta, captured)
	if err != nil {
		t.Fatalf("buildEnvelope: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if got := parsed["captured_at"]; got != "2026-05-11T17:23:45Z" {
		t.Errorf("captured_at = %v, want 2026-05-11T17:23:45Z", got)
	}
	req, ok := parsed["request"].(map[string]any)
	if !ok {
		t.Fatalf("request not an object: %T", parsed["request"])
	}
	if req["model"] != "claude-sonnet-4-7" {
		t.Errorf("request.model = %v, want claude-sonnet-4-7", req["model"])
	}
}

// TestBuildEnvelope_RawBodyFallback: if the captured body is not valid
// JSON, the envelope must include it under raw_body and omit request.
func TestBuildEnvelope_RawBodyFallback(t *testing.T) {
	out, err := buildEnvelope(envelopeMeta{
		ClaudeVersion: "2.1.138",
		CapturedAt:    time.Now().UTC(),
	}, []byte("not json at all"))
	if err != nil {
		t.Fatalf("buildEnvelope: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("envelope itself not JSON: %v", err)
	}
	if _, has := parsed["request"]; has {
		t.Error("request field present on malformed body; should be absent")
	}
	if parsed["raw_body"] != "not json at all" {
		t.Errorf("raw_body = %v, want 'not json at all'", parsed["raw_body"])
	}
}
```

- [ ] **Step 2: Run — expect FAIL (undefined types)**

```bash
go test ./... -run TestBuildEnvelope
```

Expected: compile error.

- [ ] **Step 3: Implement envelope construction**

Add to `main.go`:

```go
// envelopeMeta is the wrapper metadata captured alongside the request body.
// Its JSON form is the top-level object promptdump writes out.
type envelopeMeta struct {
	CapturedAt    time.Time `json:"-"` // formatted into captured_at by buildEnvelope
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
```

Add the `time` import if not yet present.

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./... -run TestBuildEnvelope -v
```

Expected: both TestBuildEnvelope and TestBuildEnvelope_RawBodyFallback pass.

- [ ] **Step 5: Commit**

```bash
git add utils/promptdump/main.go utils/promptdump/main_test.go
git commit -m "$(cat <<'EOF'
feat(promptdump): envelope JSON construction

buildEnvelope wraps captured body with captured_at + claude_version +
claude_path + claude_args + host metadata. Valid JSON body lands under
request; malformed body falls back to raw_body + parse_error so output
is always usable.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Production capture server (random port, real http.Server)

`captureServer.start` today uses `httptest`. Add a production-side method that binds a real `http.Server` to `127.0.0.1:0` and returns the port + a shutdown hook.

**Files:**
- Modify: `utils/promptdump/main.go`
- Modify: `utils/promptdump/main_test.go`

- [ ] **Step 1: Write the failing test**

Append to `main_test.go`:

```go
// TestCaptureServer_ListenLocalhost: production-side listen helper binds
// to 127.0.0.1 on a random port, returns a working URL, and is
// shutdownable.
func TestCaptureServer_ListenLocalhost(t *testing.T) {
	srv := newCaptureServer()
	addr, shutdown, err := srv.listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer shutdown(context.Background())

	if !strings.HasPrefix(addr, "http://127.0.0.1:") {
		t.Errorf("addr = %q, want http://127.0.0.1: prefix", addr)
	}

	resp, err := http.Post(addr+"/v1/messages",
		"application/json", strings.NewReader(`{"ping":true}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run — expect FAIL (undefined listen)**

```bash
go test ./... -run TestCaptureServer_ListenLocalhost
```

Expected: compile error.

- [ ] **Step 3: Implement `listen`**

Add to `main.go`:

```go
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
```

Add `net` to imports.

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./... -v
```

Expected: all tests so far pass.

- [ ] **Step 5: Commit**

```bash
git add utils/promptdump/main.go utils/promptdump/main_test.go
git commit -m "$(cat <<'EOF'
feat(promptdump): production-side listen helper

captureServer.listen binds to 127.0.0.1 on a random port and returns the
base URL plus a shutdown hook. ReadHeaderTimeout 5s; Serve runs in a
goroutine and is stopped via the returned shutdown(ctx).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Claude spawn + env override (orchestrator skeleton)

Wire the capture server to a real `claude` subprocess, with timeouts and clean shutdown. This task is the first that actually invokes the host's `claude` binary, so the test is more an "integration smoke" than a pure unit — guarded by `testing.Short()` so `go test -short ./...` skips it on machines without claude.

**Files:**
- Modify: `utils/promptdump/main.go`
- Modify: `utils/promptdump/main_test.go`

- [ ] **Step 1: Write the failing test**

Append to `main_test.go`:

```go
// TestRunCapture_Smoke: end-to-end against the real claude binary.
// Skipped in -short mode and when claude is not on PATH.
func TestRunCapture_Smoke(t *testing.T) {
	if testing.Short() {
		t.Skip("smoke test requires real claude binary")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := runCapture(ctx, runOpts{
		Prompt:      "ping",
		ExtraArgs:   nil,
		NoPOSTAfter: 8 * time.Second,
		Verbose:     false,
	})
	if err != nil {
		t.Fatalf("runCapture: %v", err)
	}

	var envelope map[string]any
	if err := json.Unmarshal(out, &envelope); err != nil {
		t.Fatalf("envelope not valid JSON: %v\n%s", err, out)
	}
	req, ok := envelope["request"].(map[string]any)
	if !ok {
		t.Fatalf("envelope.request not an object; envelope=%v", envelope)
	}
	if _, has := req["model"]; !has {
		t.Errorf("envelope.request.model missing; keys=%v", mapKeys(req))
	}
	if _, has := req["system"]; !has {
		t.Errorf("envelope.request.system missing; keys=%v", mapKeys(req))
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
```

- [ ] **Step 2: Run — expect FAIL (undefined types)**

```bash
go test ./... -run TestRunCapture_Smoke
```

Expected: compile error, `runCapture`/`runOpts` undefined.

- [ ] **Step 3: Implement `runCapture`**

Add to `main.go`:

```go
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
```

Add imports as needed: `context`, `os`, `os/exec`, `runtime`, `syscall`.

- [ ] **Step 4: Run — expect PASS (or SKIP if claude not on PATH)**

```bash
go test ./... -v -timeout 60s
```

Expected: TestRunCapture_Smoke PASS or SKIP (with a clear skip reason); all other tests pass.

- [ ] **Step 5: Commit**

```bash
git add utils/promptdump/main.go utils/promptdump/main_test.go
git commit -m "$(cat <<'EOF'
feat(promptdump): runCapture orchestrator with claude spawn

End-to-end: starts proxy, spawns claude with ANTHROPIC_BASE_URL and
dummy API key, waits on srv.done or NoPOSTAfter, builds envelope from
captured body. 3s grace before SIGTERM after capture. Smoke test
skipped in -short mode and when claude is not on PATH.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: CLI flag parsing + main glue (TDD)

Wire the binary's user-facing surface: flags, `--` passthrough, stdout vs `-o`, exit codes.

**Files:**
- Modify: `utils/promptdump/main.go`
- Modify: `utils/promptdump/main_test.go`

- [ ] **Step 1: Write the failing test**

Append to `main_test.go`:

```go
// TestParseFlags: covers the CLI surface contract independent of HTTP /
// exec. Verifies -- splits promptdump flags from claude passthrough,
// and default values.
func TestParseFlags(t *testing.T) {
	tests := []struct {
		name     string
		argv     []string
		wantOpts runOpts
		wantOut  string
	}{
		{
			name:     "defaults",
			argv:     []string{"promptdump"},
			wantOpts: runOpts{Prompt: "ping", NoPOSTAfter: 8 * time.Second},
			wantOut:  "",
		},
		{
			name:     "prompt and output",
			argv:     []string{"promptdump", "-p", "hi", "-o", "/tmp/x.json"},
			wantOpts: runOpts{Prompt: "hi", NoPOSTAfter: 8 * time.Second},
			wantOut:  "/tmp/x.json",
		},
		{
			name: "passthrough after double-dash",
			argv: []string{"promptdump", "-v", "--", "--model", "sonnet", "--effort", "medium"},
			wantOpts: runOpts{
				Prompt:      "ping",
				ExtraArgs:   []string{"--model", "sonnet", "--effort", "medium"},
				NoPOSTAfter: 8 * time.Second,
				Verbose:     true,
			},
			wantOut: "",
		},
		{
			name:     "keep-going",
			argv:     []string{"promptdump", "--keep-going"},
			wantOpts: runOpts{Prompt: "ping", NoPOSTAfter: 8 * time.Second, KeepGoing: true},
			wantOut:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, out, err := parseFlags(tt.argv)
			if err != nil {
				t.Fatalf("parseFlags: %v", err)
			}
			if !reflect.DeepEqual(opts, tt.wantOpts) {
				t.Errorf("opts = %+v, want %+v", opts, tt.wantOpts)
			}
			if out != tt.wantOut {
				t.Errorf("out = %q, want %q", out, tt.wantOut)
			}
		})
	}
}
```

Add `reflect` to the test imports.

- [ ] **Step 2: Run — expect FAIL**

```bash
go test ./... -run TestParseFlags
```

Expected: compile error, `parseFlags` undefined.

- [ ] **Step 3: Implement `parseFlags`**

Add to `main.go`:

```go
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
```

Add `flag` to imports.

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./... -run TestParseFlags -v
```

Expected: all four subtests pass.

- [ ] **Step 5: Rewrite `main` to use the wired-up pipeline**

Replace the placeholder `main()` with:

```go
func main() {
	opts, outPath, err := parseFlags(os.Args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "promptdump: %v\n", err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	envelope, err := runCapture(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "promptdump: %v\n", err)
		os.Exit(1)
	}

	if outPath == "" {
		os.Stdout.Write(envelope)
		os.Stdout.Write([]byte("\n"))
		return
	}
	if err := os.WriteFile(outPath, envelope, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "promptdump: write %s: %v\n", outPath, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "promptdump: wrote %s\n", outPath)
}
```

- [ ] **Step 6: Verify gofmt + vet + all tests**

```bash
gofmt -l .
go vet ./...
go test ./... -v -timeout 60s
```

Expected: no formatting issues; no vet complaints; all tests pass or skip cleanly.

- [ ] **Step 7: Commit**

```bash
git add utils/promptdump/main.go utils/promptdump/main_test.go
git commit -m "$(cat <<'EOF'
feat(promptdump): CLI surface and main glue

parseFlags handles -p / -o / -v / --keep-going and splits promptdump
flags from claude passthrough at the -- separator. main wires
parseFlags -> runCapture -> stdout or -o file, with a 15s overall
deadline and clear non-zero exits.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: `utils/promptdump/README.md`

**Files:**
- Create: `utils/promptdump/README.md`

- [ ] **Step 1: Write the README**

```markdown
# promptdump

Capture Claude Code's Anthropic Messages API request body — the verbatim
default system prompt, tool definitions, and initial user message — so
you can study and borrow from it when designing mind-form prompts.

## How it works

`promptdump` starts a tiny HTTP server on a random localhost port, then
spawns `claude -p <prompt>` with these env overrides:

```
ANTHROPIC_BASE_URL=http://127.0.0.1:<random>
ANTHROPIC_API_KEY=sk-dummy-promptdump
```

Claude's SDK posts to our local server instead of `api.anthropic.com`.
We capture the first POST to `/v1/messages`, return a minimal valid
SSE stream so claude exits cleanly, and write a JSON envelope to stdout
(or `-o <file>`).

The captured body is the verbatim request body — we add no
interpretation layer. Use `jq` to slice it.

## Build / run

`utils/promptdump` is intentionally outside the root `go.work`, so all
`go` invocations need `GOWORK=off` (Go otherwise applies the parent
workspace and refuses with "main module ... does not contain package
...").

From inside `utils/promptdump/`:

```sh
GOWORK=off go build .          # produces ./promptdump (gitignored)
GOWORK=off go run .            # default: Opus + no extra flags
GOWORK=off go run . -o snap.json
```

From the repo root:

```sh
GOWORK=off go -C utils/promptdump run .
GOWORK=off go -C utils/promptdump run . -o snap.json
```

## Useful invocations

```sh
cd utils/promptdump

# Capture under the eidopsyche mindform defaults (sonnet + medium effort).
GOWORK=off go run . -- --model sonnet --effort medium

# Compare with identity injection.
GOWORK=off go run . -o /tmp/with-identity.json -- \
    --model sonnet --append-system-prompt "$(cat identity.md)"

# Compare normal vs --bare mode (what --bare strips).
GOWORK=off go run . -o /tmp/normal.json
GOWORK=off go run . -o /tmp/bare.json -- --bare
diff <(jq -S . /tmp/normal.json) <(jq -S . /tmp/bare.json) | head -200
```

## Reading the output

```sh
# The default system prompt segments (cache-control'd).
jq '.request.system' snapshot.json

# Just the prose, no metadata.
jq -r '.request.system[].text' snapshot.json

# Tool catalogue.
jq '.request.tools | map(.name)' snapshot.json

# First user message (includes additionalContext / session hooks).
jq '.request.messages[0]' snapshot.json
```

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-p <text>` | `ping` | Prompt passed to claude as `-p <prompt>`. |
| `-o <path>` | _stdout_ | Write envelope JSON to this file instead. |
| `-v` | off | Log proxy traffic and claude stderr. |
| `--keep-going` | off | Do not SIGTERM claude after capture; let it finish on its own. |
| `--` | — | Anything after this is passed verbatim to `claude`. |

## Privacy caveat

The captured request body contains **your local context**: cwd, git
status, the contents of any `CLAUDE.md` in scope, environment hints,
your installed plugins. Don't commit raw captures to a public repo
without redacting them. If you want a "clean" snapshot, run from a
neutral working dir (e.g. `cd /tmp && go run /path/to/utils/promptdump`).

## Limitations

- Linux / macOS only. Windows process and signal handling not covered.
- Captures only the first `/v1/messages` POST. Multi-turn dynamics are
  out of scope — the first request is the informative one.
- Not a release artifact. This binary is not built by CI and not shipped
  with eidos; it's a dev-only tool that lives in `utils/`.
```

- [ ] **Step 2: Commit**

```bash
git add utils/promptdump/README.md
git commit -m "$(cat <<'EOF'
docs(promptdump): README with usage examples and privacy caveat

Covers how the proxy redirect works, useful invocations (sonnet+effort,
identity injection, --bare diff), jq slicing recipes, full flag table,
and a note about captures containing local cwd/git/CLAUDE.md context.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: Update root `CLAUDE.md` for `utils/` policy

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Read the current project structure section**

```bash
grep -n "Project Structure" CLAUDE.md
```

Locate the directory tree block (`├── docker/`, `├── README.md`, …).

- [ ] **Step 2: Insert `utils/` into the tree and add a subsection**

In the tree under `## Project Structure & Module Organization`, add this line after `├── docker/` and before `├── install.sh` (preserve box-drawing alignment):

```
├── utils/                    # Dev-only Go utilities (independent modules, not in go.work, not shipped)
```

Then add a new subsection right after the `## Prefab catalogue (top-level prefab/)` section:

```markdown
## `utils/` (dev-only utilities)

Standalone Go utilities used during development — currently one:
`utils/promptdump/`, which captures Claude Code's Anthropic Messages API
request body so the verbatim default system prompt can be studied. See
`utils/promptdump/README.md`.

Policy:

- Each utility is its own Go module. None are listed in the root `go.work`.
- The eidos build/test surface (`go build ./cmd/eidos`, `go test ./...`)
  does not touch `utils/`. Utilities are invoked explicitly via
  `go run ./utils/<name>` from the utility's own module.
- Not built by CI by default; not released. Authors add their own CI job
  if they want one.
- No stability contract — utilities may be deleted at any time.
```

- [ ] **Step 3: Sanity-check `AGENTS.md` symlink**

CLAUDE.md notes that `AGENTS.md` and `CLAUDE.md` are kept in sync via a symlink. Verify:

```bash
ls -l AGENTS.md
```

Expected: `AGENTS.md -> CLAUDE.md` (or vice versa). If broken, repair.

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md
git commit -m "$(cat <<'EOF'
docs: document utils/ policy in CLAUDE.md

Adds utils/ to the project-structure tree and a subsection explaining
the dev-only-utilities convention: independent modules, not in go.work,
not built by CI, not shipped, no stability contract.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: Final verification + manual smoke

- [ ] **Step 1: Run the full test suite**

```bash
cd /data/eidopsyche/.claude/worktrees/feat-utils-promptdump/utils/promptdump
go test ./... -v -timeout 60s
```

Expected: all tests pass; `TestRunCapture_Smoke` either passes or skips with a clear reason.

- [ ] **Step 2: Manual smoke — default capture**

```bash
go run . > /tmp/promptdump-default.json
jq '.captured_at, .claude_version, (.request.system | length), (.request.tools | length)' /tmp/promptdump-default.json
```

Expected: an RFC3339 timestamp, a non-empty version string, both lengths > 0. Inspect `.request.system` and confirm it contains the expected "You are Claude Code" preamble.

- [ ] **Step 3: Manual smoke — passthrough**

```bash
go run . -o /tmp/promptdump-sonnet.json -- --model sonnet --effort medium
jq '.request.model' /tmp/promptdump-sonnet.json
```

Expected: a string starting with `claude-sonnet-`.

- [ ] **Step 4: Verify root workspace untouched**

```bash
cd /data/eidopsyche/.claude/worktrees/feat-utils-promptdump
go build ./cmd/eidos
go test ./... -short -count=1 -timeout 5m
```

Expected: eidos still builds and unit tests still pass. `utils/promptdump` should **not** appear in the package list because it's not in `go.work`.

- [ ] **Step 5: Final lint pass**

```bash
gofmt -l .
go vet ./...
```

Expected: no output from the workspace; `utils/promptdump` is its own module so run there separately:

```bash
cd utils/promptdump
gofmt -l .
go vet ./...
```

- [ ] **Step 6: Push and open PR**

```bash
cd /data/eidopsyche/.claude/worktrees/feat-utils-promptdump
git push -u origin feat/utils-promptdump
gh pr create --title "feat(utils): promptdump — Claude Code system-prompt capture utility" --body "$(cat <<'EOF'
## Summary
- Adds a new top-level `utils/` directory for dev-only Go utilities,
  with the first inhabitant `utils/promptdump`.
- `promptdump` captures Claude Code's Anthropic Messages API request
  body — the verbatim default system prompt, tools, and first user
  message — under any combination of `claude` flags.
- Mechanism: local HTTP server + `ANTHROPIC_BASE_URL` / `ANTHROPIC_API_KEY`
  env overrides. No reverse engineering of the claude binary.
- Spec: `docs/superpowers/specs/2026-05-11-utils-promptdump-design.md`

## Test plan
- [x] Unit tests for capture-server happy path, non-messages routing,
      body-size cap, envelope construction, listen helper, parseFlags.
- [x] Integration smoke test against real claude (skipped in `-short`
      and when claude not on PATH).
- [x] Manual smoke: default capture; `--model sonnet --effort medium`
      passthrough; jq slicing of `.request.system` confirms "You are
      Claude Code" preamble present.
- [x] Verified eidos workspace (`go build ./cmd/eidos`, `go test ./...
      -short`) is unaffected — utils/ is its own module, not in
      go.work.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 7: Watch CI**

```bash
gh pr checks --watch
```

Expected: all checks green. If CI fails, diagnose root cause (not in `utils/` since it's outside go.work, so failures are likely unrelated drift).

- [ ] **Step 8: Print PR URL for the user**

After CI passes, print the PR URL so the user can review/merge:

```bash
gh pr view --json url -q .url
```

---

## Self-review notes

Spec coverage: scaffold (Task 2) ✓; capture server (Task 3) ✓; non-`/v1/messages` routing (Task 4) ✓; body-too-large cap (Task 5) ✓; envelope w/ raw_body fallback (Task 6) ✓; production listen (Task 7) ✓; orchestrator + claude spawn + timeouts (Task 8) ✓; CLI w/ `-p`/`-o`/`-v`/`--keep-going`/`--` (Task 9) ✓; README (Task 10) ✓; CLAUDE.md update (Task 11) ✓; final verification + PR (Task 12) ✓.

No-POST timeout is exercised implicitly via the orchestrator's 8s default; the design's stand-alone "no-POST timeout" test from spec §Testing is folded into TestRunCapture_Smoke's negative path being unreachable in a healthy environment — adding a separate test would require mocking `exec.LookPath` and a fake child that never POSTs, which is more machinery than the bug it would catch. The `runOpts.NoPOSTAfter` field is configurable so a regression here is straightforward to add later if it bites.

No placeholders. No "TBD". Every code block is complete enough to paste-and-run. Type names match across tasks (`captureServer`, `runOpts`, `envelopeMeta`, `hostInfo`, `runCapture`, `buildEnvelope`, `parseFlags`).
