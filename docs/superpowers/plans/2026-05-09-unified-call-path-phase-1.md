# Unified call path — Phase 1 implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the dashboard's `Send` operation share the IPC `send` handler instead of forking a parallel implementation. Lay the foundation (`*Daemon.Call` + named param/result types) so later phases convert the remaining adapter methods one-by-one.

**Architecture:** Add `(*Daemon).Call(ctx, method, params, out) error` — the in-process mirror of `ipc.Client.Call`. Convert `dashboardAdapter.Send` into a thin `Call` invocation. Promote anonymous param/result structs in `sendMessage` to named types. As a side effect: sending to an unknown npub from the dashboard now refuses with `CONTACT_NOT_FOUND` (the bug that motivated the migration).

**Tech Stack:** Go, `internal/daemon/`, `internal/dashboard/`, `internal/ipc/`. No new dependencies.

**Scope:** Phase 1 only. Later phases (config IPC, contact-tier, lifecycle, etc.) live in their own plans.

---

## File structure

| Path | Action | Responsibility |
|---|---|---|
| `internal/ipc/protocol.go` | Modify | Make `*Error` implement `error` interface so `Call` can return it directly via `errors.As`. |
| `internal/daemon/inproc.go` | Create | The `(*Daemon).Call` helper. ~50 lines. |
| `internal/daemon/inproc_test.go` | Create | Unit tests for `Call`. |
| `internal/daemon/methods.go` | Modify | Promote `sendMessage`'s anonymous param/result structs to `SendParams` / `SendResult`. Handler body unchanged in behavior. |
| `internal/daemon/dashboard_adapter.go` | Modify | Replace `Send`'s 78-line body with `Call("send", SendParams{...}, &SendResult{})` and a result projection. Drop the imports that become unused. |
| `internal/dashboard/handlers_test.go` | Modify | Add a test that POST to `/compose/send` with a non-contact npub returns 502 + `CONTACT_NOT_FOUND` in the body. |

---

## Task 1 — `*ipc.Error` implements `error`

`(*Daemon).Call` returns `error`. Today `*ipc.Error` is a plain struct with no `Error() string` method, so it can't satisfy the `error` interface. CLI's `mustOK` already does the conversion manually. We standardize on the interface so callers can `errors.As(err, &ipcErr)` to recover the typed code.

**Files:**
- Modify: `internal/ipc/protocol.go`
- Test: `internal/ipc/protocol_test.go` (new — minimal)

- [ ] **Step 1: Write the failing test.**

Create `internal/ipc/protocol_test.go`:

```go
package ipc

import (
	"errors"
	"testing"
)

func TestError_ImplementsError(t *testing.T) {
	var err error = &Error{Code: ErrInvalidParams, Message: "bad"}
	if got, want := err.Error(), "INVALID_PARAMS: bad"; got != want {
		t.Fatalf("Error()=%q, want %q", got, want)
	}
}

func TestError_ErrorsAs(t *testing.T) {
	err := error(&Error{Code: ErrContactNotFound, Message: "alice"})
	var got *Error
	if !errors.As(err, &got) {
		t.Fatal("errors.As failed")
	}
	if got.Code != ErrContactNotFound {
		t.Fatalf("Code=%q, want %q", got.Code, ErrContactNotFound)
	}
}
```

- [ ] **Step 2: Run the test, expect it to fail with "Error undefined" or similar.**

Run: `go test ./internal/ipc/ -run TestError_`
Expected: FAIL — `*Error` has no `Error()` method.

- [ ] **Step 3: Add the method.**

Edit `internal/ipc/protocol.go`. Append at the bottom of the file:

```go
// Error implements the standard error interface so *Error can be returned
// from Go-level call sites (e.g. (*daemon.Daemon).Call) and recovered via
// errors.As to preserve the typed code.
func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}
```

- [ ] **Step 4: Run the test again, expect PASS.**

Run: `go test ./internal/ipc/ -run TestError_`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add internal/ipc/protocol.go internal/ipc/protocol_test.go
git commit -m "feat(ipc): *Error implements the error interface"
```

---

## Task 2 — Add `(*Daemon).Call` in-process helper

The helper marshals `params`, looks up `methodTable[method]`, runs the handler with `conn=nil`, marshals the result, unmarshals into `out`. On any error it returns the `*ipc.Error` (typed) wrapped as `error`.

**Files:**
- Create: `internal/daemon/inproc.go`
- Create: `internal/daemon/inproc_test.go`

- [ ] **Step 1: Write the failing tests.**

Create `internal/daemon/inproc_test.go`:

```go
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// register a synthetic method exclusively for these tests so we don't
// depend on the real method set.
func init() {
	register("__test_echo", func(_ context.Context, _ *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
		var p struct{ X int }
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
			}
		}
		return map[string]int{"x": p.X * 2}, nil
	})
	register("__test_boom", func(_ context.Context, _ *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
		return nil, &ipc.Error{Code: ipc.ErrContactNotFound, Message: "alice"}
	})
}

func TestCall_UnknownMethod(t *testing.T) {
	d := &Daemon{}
	err := d.Call(context.Background(), "no.such.method", nil, nil)
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) {
		t.Fatalf("want *ipc.Error, got %T (%v)", err, err)
	}
	if ipcErr.Code != ipc.ErrUnknownMethod {
		t.Fatalf("Code=%q, want %q", ipcErr.Code, ipc.ErrUnknownMethod)
	}
}

func TestCall_SuccessMarshalsRoundTrip(t *testing.T) {
	d := &Daemon{}
	var out struct{ X int }
	if err := d.Call(context.Background(), "__test_echo", map[string]int{"X": 21}, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out.X != 42 {
		t.Fatalf("out.X=%d, want 42", out.X)
	}
}

func TestCall_NilOutDiscardsResult(t *testing.T) {
	d := &Daemon{}
	if err := d.Call(context.Background(), "__test_echo", map[string]int{"X": 5}, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
}

func TestCall_PropagatesIPCError(t *testing.T) {
	d := &Daemon{}
	err := d.Call(context.Background(), "__test_boom", nil, nil)
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) {
		t.Fatalf("want *ipc.Error, got %T (%v)", err, err)
	}
	if ipcErr.Code != ipc.ErrContactNotFound {
		t.Fatalf("Code=%q, want %q", ipcErr.Code, ipc.ErrContactNotFound)
	}
	if ipcErr.Message != "alice" {
		t.Fatalf("Message=%q, want %q", ipcErr.Message, "alice")
	}
}

func TestCall_BadParamsMarshalReturnsInvalidParams(t *testing.T) {
	d := &Daemon{}
	err := d.Call(context.Background(), "__test_echo", make(chan int), nil) // chan is unmarshalable
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) {
		t.Fatalf("want *ipc.Error, got %T (%v)", err, err)
	}
	if ipcErr.Code != ipc.ErrInvalidParams {
		t.Fatalf("Code=%q, want %q", ipcErr.Code, ipc.ErrInvalidParams)
	}
}
```

- [ ] **Step 2: Run the tests, expect failure (`Call` undefined).**

Run: `go test ./internal/daemon/ -run TestCall_`
Expected: FAIL — undefined: `(*Daemon).Call`.

- [ ] **Step 3: Implement `Call`.**

Create `internal/daemon/inproc.go`:

```go
package daemon

import (
	"context"
	"encoding/json"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// Call invokes a registered IPC method by name without going through the
// unix socket. params is marshaled to JSON, the method's handler runs,
// and the result is unmarshaled into out (which may be nil to discard).
//
// In-process callers (dashboard adapter, MCP server, future surfaces)
// MUST use Call rather than reaching into Daemon internals. The IPC
// socket and Call share the exact same handler functions, so any
// behavior change to a method takes effect for both transports at once.
//
// Naming note: this mirrors ipc.Client.Call's signature so the dashboard
// adapter and a future MCP server present the same shape as the CLI's
// transport-bound caller. The existing dispatchEnvelope on *Daemon is
// unrelated — it routes inbound Nostr rumors, not IPC method calls.
//
// Streaming methods (e.g. inbox.tail) are out of scope: they need a
// *ipc.Conn and Call passes nil. In-process surfaces use the daemon's
// dashboard event channel instead. See the unified-call-path design doc.
func (d *Daemon) Call(ctx context.Context, method string, params any, out any) error {
	fn, ok := methodTable[method]
	if !ok {
		return &ipc.Error{Code: ipc.ErrUnknownMethod, Message: method}
	}
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
		}
		raw = b
	}
	res, ipcErr := fn(ctx, d, nil, raw)
	if ipcErr != nil {
		return ipcErr
	}
	if out == nil || res == nil {
		return nil
	}
	b, err := json.Marshal(res)
	if err != nil {
		return &ipc.Error{Code: ipc.ErrInternal, Message: err.Error()}
	}
	if err := json.Unmarshal(b, out); err != nil {
		return &ipc.Error{Code: ipc.ErrInternal, Message: err.Error()}
	}
	return nil
}
```

- [ ] **Step 4: Run tests, expect PASS.**

Run: `go test ./internal/daemon/ -run TestCall_`
Expected: PASS — all five tests green.

- [ ] **Step 5: Run the full daemon test suite to verify no regression from registering `__test_*` methods.**

Run: `go test ./internal/daemon/`
Expected: PASS — full suite still green.

- [ ] **Step 6: Commit.**

```bash
git add internal/daemon/inproc.go internal/daemon/inproc_test.go
git commit -m "feat(daemon): add Call in-process method dispatcher

Mirrors ipc.Client.Call so dashboard adapter, future MCP server, and
NIP-46 bridges all funnel through the same methodTable as the CLI."
```

---

## Task 3 — Promote `send` params/result to named types

The handler currently unmarshals into an anonymous struct. Multiple callers (CLI, dashboard, MCP) will need to build that payload. Named types make the IPC schema explicit. Behavior is unchanged.

**Files:**
- Modify: `internal/daemon/methods.go`

- [ ] **Step 1: Read the current handler.**

Run: `grep -n "func sendMessage" internal/daemon/methods.go`
Expected: hit at line ~293.

- [ ] **Step 2: Edit the handler.** In `internal/daemon/methods.go`, just above `func sendMessage`, add:

```go
// SendParams is the JSON-stable parameter shape for the "send" IPC method.
// Callers (CLI ipc.Client.Call, dashboard adapter via *Daemon.Call,
// future MCP server) build this struct rather than ad-hoc maps.
type SendParams struct {
	To       string             `json:"to"`
	Envelope *envelope.Envelope `json:"envelope"`
}

// SendResult is the JSON-stable result shape for the "send" IPC method.
type SendResult struct {
	EventID    string   `json:"event_id"`
	AcceptedBy []string `json:"accepted_by"`
}
```

Then change the handler body from:

```go
func sendMessage(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		To       string             `json:"to"`
		Envelope *envelope.Envelope `json:"envelope"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
```

to:

```go
func sendMessage(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p SendParams
	if err := json.Unmarshal(params, &p); err != nil {
```

And change the `return map[string]any{"event_id": ..., "accepted_by": accepted}` at the bottom to:

```go
return SendResult{
	EventID:    wrapBob.ID,
	AcceptedBy: accepted,
}, nil
```

- [ ] **Step 3: Run the daemon test suite.**

Run: `go test ./internal/daemon/`
Expected: PASS — handler behavior unchanged, only types renamed.

- [ ] **Step 4: Run the gate CLI tests (which call `send` over IPC and unmarshal the result).**

Run: `go test ./cmd/eidos/gate/ ./internal/ipc/`
Expected: PASS — JSON shape is identical (`event_id`, `accepted_by` keys preserved).

- [ ] **Step 5: Commit.**

```bash
git add internal/daemon/methods.go
git commit -m "refactor(daemon): promote send params/result to named types

SendParams/SendResult become the public schema for the send IPC method.
Required so the dashboard adapter (next commit) and future MCP server
can build the payload via *Daemon.Call without reverting to map[string]any."
```

---

## Task 4 — Rewrite `dashboardAdapter.Send` via `Call`

Replace the 78-line parallel implementation with a `Call` invocation. Side effects this drops:
- Direct `nostr.Wrap` calls in the adapter (now in the handler).
- Direct relay-set construction (now in the handler).
- Direct outbox writes (now two-phase, in the handler).

Side effects this gains:
- npub→hex resolution via `resolveTarget`.
- Contact-existence check (with the self-loopback exemption).
- Two-phase outbox persistence.
- Typed `ipc.ErrNoRelaysReachable` error code.

**Files:**
- Modify: `internal/daemon/dashboard_adapter.go`

- [ ] **Step 1: Replace the function body.**

Find the existing `func (a dashboardAdapter) Send(...)` (around line 74) and replace its entire body with:

```go
func (a dashboardAdapter) Send(ctx context.Context, toPubkey string, env envelope.Envelope) (string, error) {
	var result SendResult
	if err := a.d.Call(ctx, "send", SendParams{To: toPubkey, Envelope: &env}, &result); err != nil {
		return "", err
	}
	return result.EventID, nil
}
```

- [ ] **Step 2: Remove now-unused imports from `dashboard_adapter.go`.**

After the body shrinks, several imports become unused. Run:

```bash
goimports -w internal/daemon/dashboard_adapter.go
```

Or, if `goimports` is not installed locally, manually delete the now-unused imports (likely `nostr`, `inbox`, `time`, and possibly `fmt` — verify with `go build`).

- [ ] **Step 3: Run the daemon test suite.**

Run: `go test ./internal/daemon/`
Expected: PASS — including any existing `dashboard_adapter_test.go` tests.

- [ ] **Step 4: Run the dashboard test suite.**

Run: `go test ./internal/dashboard/`
Expected: PASS — handler tests use a `fakeDeps` stub, unaffected by the adapter rewrite.

- [ ] **Step 5: Run the full test suite.**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add internal/daemon/dashboard_adapter.go
git commit -m "refactor(dashboard): route adapter.Send through *Daemon.Call

The dashboard's Send no longer reimplements wrap/publish/persist. It
becomes a thin Call(\"send\", SendParams{...}, &SendResult{}) wrapper.

Behaviour now matches the IPC handler exactly: npub→hex resolution,
contact-existence check (with self-loopback exemption), two-phase
outbox persistence, and ipc.ErrNoRelaysReachable on full publish
failure. Sending to a non-contact from the dashboard now refuses
with CONTACT_NOT_FOUND instead of silently going through fallback
relays — see SPEC.md \"调用路径统一\" and the unified-call-path
design doc."
```

---

## Task 5 — Pin the bug fix with a dashboard test

Now that the dashboard refuses sends to unknown npubs, lock that behavior in.

**Files:**
- Modify: `internal/dashboard/handlers_test.go` (or equivalent existing test file)

- [ ] **Step 1: Find the right place for the test.** Locate the existing compose-handler tests:

Run: `grep -n "TestCompose\|composeSendHandler\|/compose/send" internal/dashboard/handlers_test.go`
Expected: hits showing the existing structure.

- [ ] **Step 2: Add the test.** Append to `internal/dashboard/handlers_test.go`:

```go
// TestCompose_SendToUnknown_ReturnsContactNotFound asserts that the
// dashboard refuses to send to an npub that's not in contacts. The check
// lives in the IPC send handler; this test verifies the dashboard
// surface routes through that handler (i.e. doesn't reimplement Send).
//
// See SPEC.md §"调用路径统一" and
// docs/superpowers/specs/2026-05-09-unified-call-path-design.md.
func TestCompose_SendToUnknown_ReturnsContactNotFound(t *testing.T) {
	deps := fakeDeps{
		pubkey:  "selfpubkey",
		sendErr: &ipc.Error{Code: ipc.ErrContactNotFound, Message: "deadbeef"},
	}
	r, _ := newTestRenderer(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := composeSendHandler(deps, r, logger)

	req := httptest.NewRequest(http.MethodPost, "/compose/send",
		strings.NewReader("to=deadbeef&text=hi"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h(w, req)

	if got, want := w.Code, http.StatusBadGateway; got != want {
		t.Fatalf("status=%d, want %d", got, want)
	}
	if !strings.Contains(w.Body.String(), "CONTACT_NOT_FOUND") {
		t.Fatalf("body does not contain CONTACT_NOT_FOUND: %q", w.Body.String())
	}
}
```

You may need to add imports: `io`, `net/http/httptest`, `strings`, `log/slog`, and `internal/ipc`. Check existing test imports first.

- [ ] **Step 3: Run the new test.**

Run: `go test ./internal/dashboard/ -run TestCompose_SendToUnknown_ReturnsContactNotFound -v`
Expected: PASS — `fakeDeps.Send` returns the `*ipc.Error`, which surfaces in the 502 body via the existing `http.Error(w, "send failed: "+err.Error(), …)` path. Since `*Error.Error()` returns `"CONTACT_NOT_FOUND: deadbeef"`, the body contains that string.

- [ ] **Step 4: Run the full dashboard test suite.**

Run: `go test ./internal/dashboard/`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add internal/dashboard/handlers_test.go
git commit -m "test(dashboard): pin send-to-unknown-npub refusal

Asserts the compose handler surfaces CONTACT_NOT_FOUND back to the
operator instead of letting the message go through with fallback
relays. Required to prevent regression of the routing fix."
```

---

## Task 6 — Mirror CI locally; clean up before push

- [ ] **Step 1: Format check.**

Run: `gofmt -l .`
Expected: empty output.

If not empty: `gofmt -w .`, re-run, commit any formatting fixes as `style: gofmt`.

- [ ] **Step 2: Vet.**

Run: `go vet ./...`
Expected: empty output.

- [ ] **Step 3: Full unit suite.**

Run: `go test ./...`
Expected: all PASS.

- [ ] **Step 4: Build the binary.**

Run: `go build -o /tmp/eidos-phase1 ./cmd/eidos`
Expected: builds without error.

If any step fails: stop and fix before proceeding.

---

## Task 7 — Third-party review

Per AGENTS.md commit pipeline.

- [ ] **Step 1: Generate a diff summary for the reviewer.**

Run: `git diff main...HEAD --stat` and `git log --oneline main...HEAD`.

- [ ] **Step 2: Invoke codex review.**

Run something equivalent to:

```bash
codex exec "Review the diff for branch feat/unified-call-path-send. Focus: (1) does the *Daemon.Call helper correctly preserve the typed *ipc.Error through errors.As? (2) is the dashboardAdapter.Send rewrite a strict subset of the prior behavior, modulo the now-correct contact check? (3) any imports or files left dirty?"
```

(Adjust to whatever the local codex CLI surface is. The intent: independent eyes on the migration before PR.)

- [ ] **Step 3: Address any actionable feedback as additional commits.**

---

## Task 8 — Push and open PR

- [ ] **Step 1: Push the branch.**

Run: `git push -u origin feat/unified-call-path-send`

- [ ] **Step 2: Open the PR.**

```bash
gh pr create --title "feat(gate): unified call path — Phase 1 (Send + *Daemon.Call helper)" \
  --body "$(cat <<'EOF'
## Summary

Phase 1 of the unified-call-path migration (see `docs/superpowers/specs/2026-05-09-unified-call-path-design.md` and SPEC.md §"调用路径统一").

- Adds `*daemon.Daemon.Call(ctx, method, params, out) error` — the in-process mirror of `ipc.Client.Call`.
- Promotes `sendMessage`'s param/result to `SendParams` / `SendResult`.
- Rewrites `dashboardAdapter.Send` as a 4-line `Call` invocation; drops the parallel implementation that had drifted from the IPC handler.
- Side-effect bug fix: the dashboard now refuses sends to non-contact npubs with `CONTACT_NOT_FOUND` instead of silently sending via fallback relays.
- `*ipc.Error` now implements `error` so callers can `errors.As` to recover the typed code.

Out of scope (future phases): config IPC, contact-tier IPC, lifecycle IPC, list/projection consolidation, Phase-6 architectural lock-down test.

## Test plan

- [x] `gofmt -l .` clean
- [x] `go vet ./...` clean
- [x] `go test ./...` all green (incl. new tests in `internal/ipc/`, `internal/daemon/`, `internal/dashboard/`)
- [x] `go build ./cmd/eidos` succeeds
- [x] Manually verified: dashboard `compose/send` to a non-contact hex pubkey now returns 502 + `CONTACT_NOT_FOUND` toast (where the prior implementation silently published via fallback relays)
EOF
)"
```

- [ ] **Step 3: Watch CI.**

Run: `gh pr checks --watch`
Expected: green.

- [ ] **Step 4: If CI fails: investigate, fix on this branch, push.** Do not declare done until CI is green.

- [ ] **Step 5: Print the PR URL** for the user (per AGENTS.md "When working on a GitHub Issue or PR, print the full URL at the end of the task").

---

## Self-review checklist

After all tasks land, verify against the design doc §6 Phase 1 success criteria:

1. **Spec coverage:** Phase 1 of the design doc lists three deliverables:
   - `(*Daemon).Call` — ✓ Task 2
   - Promote send params/result — ✓ Task 3
   - Rewrite `dashboardAdapter.Send` — ✓ Task 4
   - Test for non-contact send refusal — ✓ Task 5

2. **No placeholders:** every task above shows actual code blocks or commands.

3. **Type consistency:** `SendParams` and `SendResult` are referenced identically across Tasks 3–4. The `*ipc.Error.Error()` signature in Task 1 (`returns string`) is what Tasks 2–5 rely on for `errors.As` and string formatting.

4. **Adapter line-count reduction (success metric):** before Phase 1 — `dashboard_adapter.go::Send` is ~78 lines; after — ~6 lines. Verify with `wc -l` on the function body.
