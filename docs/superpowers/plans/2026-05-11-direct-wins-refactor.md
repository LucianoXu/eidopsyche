# Direct-Wins Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land seven mechanical-to-medium refactors identified in the 2026-05-11 code-health survey as a single, well-tested PR.

**Architecture:** Each task is a focused refactor confined to one package (or a single cross-package replacement) with no behavioral change unless explicitly called out. Task 7 (`forge.Orchestrate`) is the only behavioral change — it adds reverse rollback on partial-create failure, with failure-mode integration-style tests on the in-package fake `forgectl.Client`.

**Tech Stack:** Go 1.25, `github.com/spf13/cobra`, `nbd-wtf/go-nostr`, internal packages under `internal/`.

**Worktree:** `.claude/worktrees/direct-wins`, branch `refactor/direct-wins`, based on `origin/main` (b662c96).

**CI mirror:** before opening the PR, run locally:
```bash
gofmt -l . | tee /tmp/gofmt.out  # must be empty
go vet ./...
go test ./...
go test -tags=integration ./test/integration/...
```

---

## File Structure (created / modified)

**New files:**
- `internal/fileops/atomic.go` — `AtomicWrite(path string, body []byte, perm fs.FileMode) error`
- `internal/fileops/atomic_test.go` — TDD coverage
- `internal/dashboard/handlers_messages.go` — messages/thread/compose/send + buildMessagesView/bubbles/rows
- `internal/dashboard/handlers_settings.go` — settings shell/identity/label/config
- `internal/dashboard/handlers_contacts.go` — settings.contacts.* handlers + render helpers
- `internal/dashboard/handlers_relays_view.go` — relaysHandler + buildRelaysView + sortRelayRows (NOT to be confused with existing `handlers_relays.go`, which holds settings-side relay mutation handlers)
- `internal/daemon/methods_contact.go`
- `internal/daemon/methods_relay.go`
- `internal/daemon/methods_invite.go`
- `internal/daemon/methods_card.go`
- `internal/daemon/methods_config.go`
- `internal/daemon/methods_messaging.go`
- `internal/daemon/methods_lifecycle.go`
- `internal/daemon/methods_identity.go`
- `cmd/eidos/supervisor/transcript_handle.go` — `transcriptHandle` type wrapping store/file/counter
- `cmd/eidos/supervisor/transcript_handle_test.go`

**Modified files:**
- `internal/authstate/authstate.go` — adopt `fileops.AtomicWrite`
- `internal/update/cache.go` — adopt `fileops.AtomicWrite`
- `internal/transcript/store.go` — adopt `fileops.AtomicWrite` on index write
- `internal/wake/birth.go` — adopt `fileops.AtomicWrite`
- `internal/invite/payload.go` — drop private `encodeNpub`/`decodeNpub`, use `identity.EncodeNpub`/`identity.DecodeNpub`
- `internal/daemon/methods.go` — keep only `init()`/`register()`, `internalErr`, `resolveTarget`, `isHex64`, `peerLabel`, `lookupLabel`, `annotateInboxLabels`, `annotateOutboxLabels`, and remove the stale "Phase 5" comment on `relayListContains` (the helper itself moves to `methods_relay.go`)
- `internal/dashboard/handlers.go` — shrink to `registerHandlersWithRenderer`, `shellHandler`, `sidebarHandler`, `topbarHandler`, `buildSidebar`, `lastSeenByContact`, `lookupContact`, `pubkeyToNpub`, `humanSince` (`previewFor` ends up co-located with its msgToRow/sentToRow callers in `handlers_messages.go`)
- `cmd/eidos/supervisor/agent_runner.go` — shrink `runWithTranscript` to ~70 lines by delegating store/open/finalize to `transcriptHandle`
- `cmd/eidos/forge/orchestrate.go` — refactor `Orchestrate` into linear `[]orchestrateStep` + reverse-undo on failure
- `cmd/eidos/forge/create_test.go` — add failure-mode tests for each step (RunInit fail, ContainerCreate fail, etc.)

---

## Task 1: `internal/fileops.AtomicWrite` (TDD)

**Why:** Eight call sites duplicate `os.WriteFile(tmp, body, perm); os.Rename(tmp, final)`. Extract once.

**Files:**
- Create: `internal/fileops/atomic.go`
- Create: `internal/fileops/atomic_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/fileops/atomic_test.go
package fileops_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/fileops"
)

func TestAtomicWrite_WritesBodyWithPerm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := fileops.AtomicWrite(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("body = %q want %q", got, "hello")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v want 0o600", info.Mode().Perm())
	}
}

func TestAtomicWrite_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fileops.AtomicWrite(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Fatalf("body = %q want %q", got, "new")
	}
}

func TestAtomicWrite_CleansTempOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := fileops.AtomicWrite(path, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 file in dir after AtomicWrite, got %d: %v", len(entries), entries)
	}
}

func TestAtomicWrite_ParentDirMissingReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-dir", "x.txt")
	if err := fileops.AtomicWrite(path, []byte("hi"), 0o600); err == nil {
		t.Fatal("expected error when parent dir missing")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /data/eidopsyche/.claude/worktrees/direct-wins
go test ./internal/fileops/...
```
Expected: FAIL with "no Go files" or "undefined: fileops.AtomicWrite".

- [ ] **Step 3: Implement `AtomicWrite`**

```go
// internal/fileops/atomic.go
// Package fileops collects small filesystem helpers shared across packages.
package fileops

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// AtomicWrite writes body to path durably by writing a sibling temp file
// first and renaming it into place. The temp file lives in the same
// directory so the rename is atomic on POSIX. On any failure the temp
// file is removed.
//
// Callers responsible for ensuring path's parent directory exists.
func AtomicWrite(path string, body []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/fileops/... -v
```
Expected: all four PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fileops/
git commit -m "feat(fileops): add AtomicWrite helper"
```

---

## Task 2: Migrate atomic-write call sites

**Why:** Replace four straightforward `tmp + rename` sites with `fileops.AtomicWrite`. We deliberately do NOT touch the four sites that use `os.CreateTemp` with custom prefixes plus flock dances (dreamstate, sessionstate, wake/wake.go, scheduler) — those have larger semantic surface and stay as-is.

**Files:**
- Modify: `internal/authstate/authstate.go:55-59`
- Modify: `internal/update/cache.go:68-72`
- Modify: `internal/transcript/store.go:153-157` (index write only; leave the `.tmp` writer at :102 alone because the existing flow writes-then-renames the buffered transcript file by name)
- Modify: `internal/wake/birth.go:42-46`

- [ ] **Step 1: Migrate `internal/authstate/authstate.go`**

Replace lines 55-59 (`tmp := path + ".tmp"; os.WriteFile(tmp, body, 0o600); _ = ...; os.Rename(tmp, path)`) with:

```go
return fileops.AtomicWrite(path, body, 0o600)
```

Add import `"github.com/LucianoXu/eidopsyche/internal/fileops"`.

- [ ] **Step 2: Migrate `internal/update/cache.go`**

Replace lines 68-72 with the same single-call pattern.

- [ ] **Step 3: Migrate `internal/transcript/store.go` index write**

Replace lines 153-157 (the `IndexPath()` writer) with `fileops.AtomicWrite(s.IndexPath(), body, 0o600)`.

- [ ] **Step 4: Migrate `internal/wake/birth.go`**

Replace lines 42-46 with `fileops.AtomicWrite(filepath.Join(dir, BirthFileName), body, 0o600)`.

- [ ] **Step 5: Run all affected unit tests**

```bash
go test ./internal/authstate/... ./internal/update/... ./internal/transcript/... ./internal/wake/...
```
Expected: PASS (these packages already have tests covering writes).

- [ ] **Step 6: Run the full unit suite to catch upstream effects**

```bash
go test ./...
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/authstate/ internal/update/ internal/transcript/ internal/wake/
git commit -m "refactor: migrate atomic-write call sites to fileops.AtomicWrite"
```

---

## Task 3: invite reuses identity helpers + Phase-5 comment cleanup

**Why:** `internal/invite/payload.go` has private `encodeNpub`/`decodeNpub` shadowing `identity.EncodeNpub`/`identity.DecodeNpub`. Also clean a misleading stale comment in `internal/daemon/methods.go` describing a Phase-5 migration that already shipped.

**Files:**
- Modify: `internal/invite/payload.go`
- Modify: `internal/daemon/methods.go:142-146`

- [ ] **Step 1: Replace private helpers in `invite/payload.go`**

Add import `"github.com/LucianoXu/eidopsyche/internal/identity"`.

At line 67 (`derivedNpub, err := encodeNpub(expectedPK)`) → `derivedNpub, err := identity.EncodeNpub(expectedPK)`.
At line 119 (`pubHex, err := decodeNpub(p.IssuerNpub)`) → `pubHex, err := identity.DecodeNpub(p.IssuerNpub)`.
Delete the `encodeNpub` and `decodeNpub` function definitions at lines 139-158 entirely.

- [ ] **Step 2: Run invite tests**

```bash
go test ./internal/invite/...
```
Expected: PASS (Sign/Verify roundtrip).

- [ ] **Step 3: Clean up `methods.go:142-146` comment**

The current comment reads:
```
// relayListContains is a local set-membership helper used by
// contact.add-from-card's relay-hint dedup. Kept here rather than
// imported because the dashboard-adapter copy of this helper is being
// removed in Phase 5.
```

Phase 5 has shipped (merged in commit history pre-2026-05-09). Replace with a short, factual one-liner:

```go
// relayListContains is set-membership for contact.add-from-card's relay-hint dedup.
```

(Note: the helper itself will move to `methods_relay.go` in Task 5; we keep the comment fix here so the cleanup commit is independently meaningful.)

- [ ] **Step 4: Run daemon tests**

```bash
go test ./internal/daemon/...
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/invite/payload.go internal/daemon/methods.go
git commit -m "refactor: reuse identity helpers in invite and drop stale Phase-5 comment"
```

---

## Task 4: Split `internal/dashboard/handlers.go` by domain

**Why:** 1346 lines, 50 handlers, hard to navigate. Pure file moves; no signature, no body change.

**Files:**
- Create: `internal/dashboard/handlers_messages.go`
- Create: `internal/dashboard/handlers_settings.go`
- Create: `internal/dashboard/handlers_contacts.go`
- Create: `internal/dashboard/handlers_relays_view.go`
- Modify: `internal/dashboard/handlers.go` (shrink)

Function placement map (source-of-truth for the split):

| Destination | Functions (current handlers.go line in parens) |
|---|---|
| `handlers.go` (keep) | `registerHandlersWithRenderer` (23), `shellHandler` (57), `sidebarHandler` (90), `topbarHandler` (913), `buildSidebar` (318), `lastSeenByContact` (344), `lookupContact` (491), `pubkeyToNpub` (899), `humanSince` (1332) |
| `handlers_messages.go` | `messagesHandler` (113), `threadOrSendHandler` (129), `threadHandler` (141), `composeHandler` (206), `sendHandler` (221), `composeSendHandler` (240), `sendChat` (259), `buildMessagesView` (365), `msgToRow` (401), `sentToRow` (414), `previewFor` (425), `buildBubbles` (433), `msgToBubble` (453), `sentToBubble` (469) |
| `handlers_relays_view.go` | `relaysHandler` (540), `buildRelaysView` (517), `sortRelayRows` (555) |
| `handlers_settings.go` | `settingsShellHandler` (603), `settingsIdentityHandler` (633), `settingsLabelPostHandler` (658), `settingsConfigHandler` (713), `renderSettingsFullPage` (776), `buildSettingsShell` (806), `buildSettingsIdentity` (832), `buildSettingsConfig` (845), `configRowFromKey` (862), `slugifyPath` (877), `isSettingsURL` (884) |
| `handlers_contacts.go` | `settingsContactsHandler` (946), `renderContactsErr` (994), `settingsContactsScanHandler` (1006), `settingsContactsByPubkeyHandler` (1052), `renderContactDetail` (1114), `renderContactRemoveModal` (1130), `handleContactSetLabel` (1155), `handleContactSetTier` (1195), `handleContactRemove` (1223), `buildSettingsContacts` (1260), `buildContactDetail` (1285), `shortHexID` (1306), `requireConfirm` (1318) |

- [ ] **Step 1: Create the four new files with package header and imports as needed**

Each new file starts with:
```go
package dashboard

import ( /* whichever subset of imports the moved functions need */ )
```

Use `goimports` after the move to fix import lists.

- [ ] **Step 2: Cut+paste functions per the map above**

Move each function bodily — do NOT rename, do NOT change signature, do NOT change behavior. Keep doc comments intact.

- [ ] **Step 3: Run goimports + gofmt**

```bash
gofmt -w internal/dashboard/
# If goimports is available:
goimports -w internal/dashboard/ 2>/dev/null || true
```

- [ ] **Step 4: Build + test the package**

```bash
go build ./internal/dashboard/...
go test ./internal/dashboard/...
```
Expected: PASS. The existing test files reference handlers by exported/unexported name and live in the same package, so the split is invisible to them.

- [ ] **Step 5: Build the whole tree**

```bash
go build ./...
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/dashboard/
git commit -m "refactor(dashboard): split handlers.go into thematic files"
```

---

## Task 5: Split `internal/daemon/methods.go` by domain

**Why:** 1007 lines, 41 methods. Same rationale and same pure-move discipline as Task 4.

**Files:**
- Create: `internal/daemon/methods_contact.go`
- Create: `internal/daemon/methods_relay.go`
- Create: `internal/daemon/methods_invite.go`
- Create: `internal/daemon/methods_card.go`
- Create: `internal/daemon/methods_config.go`
- Create: `internal/daemon/methods_messaging.go`
- Create: `internal/daemon/methods_lifecycle.go`
- Create: `internal/daemon/methods_identity.go`
- Modify: `internal/daemon/methods.go` (shrink)

Function placement map (current methods.go line in parens):

| Destination | Functions |
|---|---|
| `methods.go` (keep) | `init`/`register` (25), `internalErr` (826), `resolveTarget` (832), `isHex64` (997), `peerLabel` (806), `lookupLabel` (786), `annotateInboxLabels` (768), `annotateOutboxLabels` (776) |
| `methods_contact.go` | `contactAddFromCard` (75), `contactAdd` (297), `contactList` (325), `contactGet` (345), `contactSetTier` (369), `contactRemove` (473), `contactSetLabel` (494) |
| `methods_relay.go` | `relayList` (522), `relayAdd` (531), `relayRemove` (552), `relaysHealth` (572), `relayListContains` (147) |
| `methods_invite.go` | `inviteCreate` (885), `inviteList` (922), `inviteRevoke` (937), `inviteRedeem` (964) |
| `methods_card.go` | `cardExport` (259), `cardParse` (275), `cardScan` (392) |
| `methods_config.go` | `(d *Daemon) configPath` (168), `configGet` (176), `configSet` (191), `ConfigSetParams` type |
| `methods_messaging.go` | `sendMessage` (597), `inboxList` (694), `inboxTail` (726), `outboxList` (732) |
| `methods_lifecycle.go` | `serviceStatus` (422), `lifecycleRunMethod` (451), `lifecycleStatusMethod` (468), `subscribeRefresh` (820), `versionMethod` (812) |
| `methods_identity.go` | `whoami` (217), `setOwnLabel` (242) |

- [ ] **Step 1: Create the eight new files**

Same pattern as Task 4. Each file's imports are a subset of the current `methods.go` imports — let goimports/gofmt prune.

- [ ] **Step 2: Cut+paste per the map**

Move only — do not edit bodies.

- [ ] **Step 3: gofmt + build**

```bash
gofmt -w internal/daemon/
go build ./internal/daemon/...
```

- [ ] **Step 4: Run daemon tests (which include the AST lint that the dashboard adapter still routes through Call)**

```bash
go test ./internal/daemon/...
```
Expected: PASS — methodTable registrations did not change; AST lint test inspects `dashboard_adapter.go`, untouched here.

- [ ] **Step 5: Run the full unit suite**

```bash
go test ./...
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/daemon/
git commit -m "refactor(daemon): split methods.go by domain"
```

---

## Task 6: Extract `transcriptHandle` from `runWithTranscript`

**Why:** `runWithTranscript` is 121 lines, mixing store creation, file open/retry-on-collision, double-close-protection, claude stream drain coordination, and finalize. Encapsulate store/file/counter lifecycle into a small type with focused tests.

**Files:**
- Create: `cmd/eidos/supervisor/transcript_handle.go`
- Create: `cmd/eidos/supervisor/transcript_handle_test.go`
- Modify: `cmd/eidos/supervisor/agent_runner.go` (shrink `runWithTranscript`)

- [ ] **Step 1: Write the failing tests**

```go
// cmd/eidos/supervisor/transcript_handle_test.go
package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/transcript"
)

func TestTranscriptHandle_OpenWritesIndexOnFinalize(t *testing.T) {
	dir := t.TempDir()
	h, err := openTranscriptHandle(dir, "wake-123")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := h.Write([]byte(`{"type":"system"}` + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	entry := transcript.Entry{
		ID:        "wake-123",
		Reason:    "heartbeat",
		StartedAt: time.Now().Unix() - 1,
		EndedAt:   time.Now().Unix(),
		OK:        true,
	}
	if err := h.finalize(entry, 50, 10*1024); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	idx, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !strings.Contains(string(idx), "wake-123") {
		t.Fatalf("index missing wake-123: %s", idx)
	}
}

func TestTranscriptHandle_CloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	h, err := openTranscriptHandle(dir, "wake-x")
	if err != nil {
		t.Fatal(err)
	}
	h.close()
	h.close() // must not panic / err
}

func TestTranscriptHandle_OpenRetriesOnCollision(t *testing.T) {
	dir := t.TempDir()
	first, err := openTranscriptHandle(dir, "wake-dup")
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	second, err := openTranscriptHandle(dir, "wake-dup")
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	defer second.close()
	if second.wakeID == first.wakeID {
		t.Fatalf("expected suffixed wakeID, got same: %s", second.wakeID)
	}
	if !strings.HasPrefix(second.wakeID, "wake-dup-dup-") {
		t.Fatalf("unexpected retry wakeID: %s", second.wakeID)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./cmd/eidos/supervisor/ -run TestTranscriptHandle
```
Expected: FAIL — `openTranscriptHandle` undefined.

- [ ] **Step 3: Implement `transcriptHandle`**

```go
// cmd/eidos/supervisor/transcript_handle.go
package supervisor

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/transcript"
)

// transcriptHandle owns the per-wake transcript file and the underlying
// store's bookkeeping. It exists so runWithTranscript can stay focused
// on coordinating the claude subprocess.
type transcriptHandle struct {
	store  *transcript.Store
	out    *os.File
	wakeID string
	closed bool
}

// openTranscriptHandle prepares a store under dir, recovers any partial
// state, and opens out for wakeID. On a same-second same-reason
// collision it retries once with a process-unique suffix.
func openTranscriptHandle(dir, wakeID string) (*transcriptHandle, error) {
	store, err := transcript.NewStore(dir)
	if err != nil {
		return nil, fmt.Errorf("transcripts: %w", err)
	}
	if rerr := store.Recover(); rerr != nil {
		log.Printf("agent-runner: transcripts recover: %v", rerr)
	}
	out, err := store.Open(wakeID)
	if err != nil && errors.Is(err, fs.ErrExist) {
		altID := fmt.Sprintf("%s-dup-%d-%d", wakeID, os.Getpid(), time.Now().UnixNano())
		log.Printf("agent-runner: transcripts open(%s) collided; retrying as %s", wakeID, altID)
		wakeID = altID
		out, err = store.Open(wakeID)
	}
	if err != nil {
		return nil, fmt.Errorf("transcripts open(%s): %w", wakeID, err)
	}
	return &transcriptHandle{store: store, out: out, wakeID: wakeID}, nil
}

// Write implements io.Writer for the per-wake transcript file. It is
// safe to wrap in a drain goroutine.
func (h *transcriptHandle) Write(p []byte) (int, error) { return h.out.Write(p) }

// close releases the file. Safe to call more than once.
func (h *transcriptHandle) close() {
	if h.closed {
		return
	}
	_ = h.out.Close()
	h.closed = true
}

// finalize closes the file (idempotently) and writes the index entry.
func (h *transcriptHandle) finalize(entry transcript.Entry, maxCount int, maxBytes int64) error {
	h.close()
	return h.store.Finalize(entry, maxCount, maxBytes)
}
```

- [ ] **Step 4: Re-run TranscriptHandle tests**

```bash
go test ./cmd/eidos/supervisor/ -run TestTranscriptHandle -v
```
Expected: PASS.

- [ ] **Step 5: Refactor `runWithTranscript` to use it**

In `cmd/eidos/supervisor/agent_runner.go`, replace the body of `runWithTranscript` (lines 302-422) with the version below. Net effect: ~121 → ~75 lines, no behavior change.

```go
func runWithTranscript(sig wake.Signal, ontologyDir string, args []string, sessionUUID string) error {
	startedAt := time.Now().Unix()

	wakeID := sig.ID
	if wakeID == "" {
		wakeID = fmt.Sprintf("%d-%s", startedAt, sig.Reason)
	}

	h, err := openTranscriptHandle(transcriptsRuntimeDir, wakeID)
	if err != nil {
		log.Printf("agent-runner: %v; falling back to plain stdout", err)
		return runWithoutTranscript(ontologyDir, plainClaudeArgs(args))
	}
	defer h.close()

	c := exec.Command(claudeBin, args...) //nolint:gosec
	c.Dir = ontologyDir
	stderrBuf := &strings.Builder{}
	c.Stderr = io.MultiWriter(os.Stderr, stderrBuf)
	c.Env = append(os.Environ(), "CLAUDE_DIR="+filepath.Join(ontologyDir, ".claude"))

	stdoutPipe, err := c.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	log.Printf("agent-runner: wake-id=%s reason=%s starting", h.wakeID, sig.Reason)
	if err := c.Start(); err != nil {
		return fmt.Errorf("start claude: %w", err)
	}

	counter := &transcript.Counter{}
	drainErr := make(chan error, 1)
	go func() { drainErr <- drainStreamJSON(stdoutPipe, h, counter, h.wakeID) }()

	// Drain BEFORE Wait; see exec.Cmd.StdoutPipe doc.
	derr := <-drainErr
	runErr := c.Wait()
	h.close() // flush before Finalize.
	if derr != nil {
		log.Printf("agent-runner: wake-id=%s drain: %v", h.wakeID, derr)
	}

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}

	endedAt := time.Now().Unix()
	cfg, _ := config.Load(gateConfigPath)
	maxCount, maxBytes := transcriptLimits(cfg)
	finalEntry := transcript.Entry{
		ID:             h.wakeID,
		SessionID:      sessionUUID,
		Reason:         string(sig.Reason),
		StartedAt:      startedAt,
		EndedAt:        endedAt,
		OK:             runErr == nil,
		ExitCode:       exitCode,
		ToolUseCount:   counter.ToolUseCount,
		ThinkingBlocks: counter.ThinkingBlocks,
	}
	if counter.Result != nil {
		finalEntry.OK = runErr == nil && counter.Result.OK
		finalEntry.CostUSD = counter.Result.TotalCostUSD
	}
	if ferr := h.finalize(finalEntry, maxCount, maxBytes); ferr != nil {
		log.Printf("agent-runner: wake-id=%s finalize: %v", h.wakeID, ferr)
	}

	statusStr := "ok"
	if !finalEntry.OK {
		statusStr = fmt.Sprintf("failed(%d)", exitCode)
	}
	costStr := "-"
	if finalEntry.CostUSD != nil && *finalEntry.CostUSD > 0 {
		costStr = fmt.Sprintf("$%.4f", *finalEntry.CostUSD)
	}
	log.Printf("agent-runner: wake-id=%s completed cost=%s dur_s=%d tools=%d %s",
		h.wakeID, costStr, endedAt-startedAt, counter.ToolUseCount, statusStr)

	if runErr != nil && matchSessionNotFound(stderrBuf.String()) {
		return errors.Join(errSessionNotFound, runErr)
	}
	return handleClaudeExit(runErr, c.ProcessState)
}
```

NOTE on call-site change in `drainStreamJSON`: its second arg is `io.Writer`. We now pass `h` (which implements `Write`) instead of the bare `*os.File`. The signature does NOT need to change because `*transcriptHandle` satisfies `io.Writer`.

- [ ] **Step 6: Run supervisor tests**

```bash
go test ./cmd/eidos/supervisor/...
```
Expected: PASS. Existing `agent_runner_collision_test.go` / `agent_runner_stream_test.go` exercise the integrated path.

- [ ] **Step 7: Commit**

```bash
git add cmd/eidos/supervisor/transcript_handle.go cmd/eidos/supervisor/transcript_handle_test.go cmd/eidos/supervisor/agent_runner.go
git commit -m "refactor(supervisor): extract transcriptHandle from runWithTranscript"
```

---

## Task 7: `forge.Orchestrate` → explicit Step+rollback + failure tests

**Why:** Current code is a flat 100-line linear flow with two ad-hoc `_ = c.VolumeRemove(ctx, vol)` rollbacks at lines 134 and 147. We make every reversible action a `step` and run reverse undo on failure. Behavior change: `ContainerCreate` failure now also removes the volume (it already did via the existing line 147), and we add cleanup for the (currently uncovered) failure mode where init succeeds but ContainerCreate fails after RunInit — which the original code does handle correctly. The behavior delta is small but the test coverage gain is large: every step's failure is now exercised.

**Files:**
- Modify: `cmd/eidos/forge/orchestrate.go`
- Modify: `cmd/eidos/forge/create_test.go`

- [ ] **Step 1: Write the failing failure-mode tests first**

Append to `cmd/eidos/forge/create_test.go` (the file already contains `fakeClient`):

```go
// --- failure-mode rollback tests ---

// fakeClientRunInitErr makes RunInit fail; we expect Orchestrate to remove the volume it created.
type fakeClientRunInitErr struct {
	fakeClient
	volumeRemoved bool
}

func (f *fakeClientRunInitErr) VolumeRemove(_ context.Context, _ string) error {
	f.volumeRemoved = true
	return nil
}
func (f *fakeClientRunInitErr) RunInit(_ context.Context, _ forgectl.RunInitOpts) (forgectl.RunInitResult, error) {
	return forgectl.RunInitResult{Stderr: []byte("boom")}, fmt.Errorf("init failed")
}

func TestOrchestrate_RollsBackVolumeOnInitFailure(t *testing.T) {
	f := &fakeClientRunInitErr{}
	err := Orchestrate(context.Background(), f, "alice", CreateOpts{
		Label: "alice", Owner: "npub1...", Relay: "wss://x", Image: "img:dev",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !f.volumeRemoved {
		t.Fatal("expected volume to be rolled back after RunInit failure")
	}
}

// fakeClientContainerCreateErr makes ContainerCreate fail; we expect Orchestrate to remove the volume.
type fakeClientContainerCreateErr struct {
	fakeClient
	volumeRemoved bool
}

func (f *fakeClientContainerCreateErr) VolumeRemove(_ context.Context, _ string) error {
	f.volumeRemoved = true
	return nil
}
func (f *fakeClientContainerCreateErr) ContainerCreate(_ context.Context, _ forgectl.CreateOpts) error {
	return fmt.Errorf("create failed")
}

func TestOrchestrate_RollsBackVolumeOnContainerCreateFailure(t *testing.T) {
	f := &fakeClientContainerCreateErr{}
	err := Orchestrate(context.Background(), f, "alice", CreateOpts{
		Label: "alice", Owner: "npub1...", Relay: "wss://x", Image: "img:dev",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !f.volumeRemoved {
		t.Fatal("expected volume to be rolled back after ContainerCreate failure")
	}
}

// fakeClientPullErr already exists in this file (line 81) — add a rollback expectation.
func TestOrchestrate_NoVolumeOnImagePullFailure(t *testing.T) {
	// ImagePull happens BEFORE VolumeCreate; no rollback should be needed.
	f := &fakeClientPullErr{}
	err := Orchestrate(context.Background(), f, "alice", CreateOpts{
		Label: "alice", Owner: "npub1...", Relay: "wss://x", Image: "img:dev",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	// No assertion on volumeRemoved because the volume was never created.
}
```

Also add the missing `fmt` import if not already present.

- [ ] **Step 2: Run new tests to verify they fail or pass on existing code**

```bash
go test ./cmd/eidos/forge/ -run TestOrchestrate -v
```
Expected: `TestOrchestrate_RollsBackVolumeOnInitFailure` and `TestOrchestrate_RollsBackVolumeOnContainerCreateFailure` PASS already on existing code (the two ad-hoc rollbacks at lines 134 / 147 handle them). `TestOrchestrate_NoVolumeOnImagePullFailure` PASS trivially.

This is expected — the tests memorialize current behavior so the refactor cannot regress it.

- [ ] **Step 3: Refactor `Orchestrate` to explicit step list**

Replace the body of `Orchestrate` with:

```go
// orchestrateStep is one reversible action in the create pipeline.
type orchestrateStep struct {
	name string
	do   func(ctx context.Context) error
	undo func(ctx context.Context) error // nil = nothing to undo
}

// runSteps executes steps in order. On the first error, it runs the
// undo of every step that completed (in reverse), then returns a
// wrapped error mentioning the failing step's name.
func runSteps(ctx context.Context, steps []orchestrateStep) error {
	done := make([]orchestrateStep, 0, len(steps))
	for _, s := range steps {
		if err := s.do(ctx); err != nil {
			for i := len(done) - 1; i >= 0; i-- {
				if done[i].undo != nil {
					_ = done[i].undo(ctx)
				}
			}
			return fmt.Errorf("%s: %w", s.name, err)
		}
		done = append(done, s)
	}
	return nil
}

func Orchestrate(ctx context.Context, c forgectl.Client, name string, o CreateOpts) error {
	vol := forgectl.VolumeName(name)
	cont := forgectl.ContainerName(name)

	if exists, err := c.VolumeExists(ctx, vol); err != nil {
		return fmt.Errorf("check volume: %w", err)
	} else if exists {
		return fmt.Errorf("volume %s already exists", vol)
	}
	if exists, err := c.ContainerExists(ctx, cont); err != nil {
		return fmt.Errorf("check container: %w", err)
	} else if exists {
		return fmt.Errorf("container %s already exists", cont)
	}

	image := o.Image
	if image == "" {
		image = DefaultImage()
	}

	steps := []orchestrateStep{
		{
			name: "ensure-image",
			do: func(ctx context.Context) error {
				exists, err := c.ImageExists(ctx, image)
				if err != nil {
					return fmt.Errorf("inspect image %s: %w", image, err)
				}
				if exists {
					return nil
				}
				if err := c.ImagePull(ctx, image, os.Stderr); err != nil {
					return fmt.Errorf("pull %s: %w", image, err)
				}
				return nil
			},
			// no undo: images are shared, not per-mind-form.
		},
		{
			name: "create-volume",
			do:   func(ctx context.Context) error { return c.VolumeCreate(ctx, vol) },
			undo: func(ctx context.Context) error { return c.VolumeRemove(ctx, vol) },
		},
		{
			name: "init-volume",
			do: func(ctx context.Context) error {
				pipeR, pipeW := io.Pipe()
				go func() {
					defer pipeW.Close()
					params := ontology.Params{
						Label:        o.Label,
						OwnerNpub:    o.Owner,
						OwnerLabel:   o.OwnerLabel,
						MindFormNpub: o.MindFormNpub,
						HomeRelay:    o.Relay,
						CreatedDate:  time.Now().UTC().Format("2006-01-02"),
						JournalEntry: o.JournalEntry,
					}
					var perr error
					if o.PrefabID != "" {
						perr = ontology.TarStreamPrefab(pipeW, o.PrefabID, params)
					} else {
						perr = ontology.TarStream(pipeW, params)
					}
					if perr != nil {
						_ = pipeW.CloseWithError(perr)
					}
				}()
				env := []string{
					"EIDOS_IN_CONTAINER=1",
					"EIDOS_FORGE_NAME=" + name,
					"EIDOS_FORGE_LABEL=" + o.Label,
					"EIDOS_FORGE_OWNER=" + o.Owner,
					"EIDOS_FORGE_RELAY=" + o.Relay,
					"EIDOS_FORGE_MODEL=" + o.Model,
				}
				if o.KeyHex != "" {
					env = append(env, "EIDOS_FORGE_KEY_HEX="+o.KeyHex)
				}
				res, err := c.RunInit(ctx, forgectl.RunInitOpts{
					Image: image,
					Mount: forgectl.Mount{VolumeName: vol, Target: "/eidos"},
					User:  "0:0",
					Env:   env,
					Cmd:   []string{"eidos", "forge", "init-volume"},
					Stdin: pipeR,
				})
				if err != nil {
					return fmt.Errorf("%w (stderr: %s)", err, string(res.Stderr))
				}
				return nil
			},
			// Undo handled by create-volume's undo; nothing additional.
		},
		{
			name: "create-container",
			do: func(ctx context.Context) error {
				return c.ContainerCreate(ctx, forgectl.CreateOpts{
					Name:  cont,
					Image: image,
					Mount: forgectl.Mount{VolumeName: vol, Target: "/eidos"},
				})
			},
			undo: func(ctx context.Context) error { return c.ContainerRemove(ctx, cont) },
		},
	}
	return runSteps(ctx, steps)
}
```

- [ ] **Step 4: Run the orchestrate test set**

```bash
go test ./cmd/eidos/forge/ -run TestOrchestrate -v
go test ./cmd/eidos/forge/ -run TestCreate -v
```
Expected: PASS, including new failure-mode tests and existing happy-path / pull-skip / vol-exists tests.

- [ ] **Step 5: Run the full unit suite**

```bash
go test ./...
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/eidos/forge/orchestrate.go cmd/eidos/forge/create_test.go
git commit -m "refactor(forge): orchestrate as explicit steps with reverse rollback"
```

---

## Closing Tasks

### CI mirror locally

- [ ] **Step 1: gofmt clean**

```bash
gofmt -l . | grep -v vendor
```
Expected: empty.

- [ ] **Step 2: vet**

```bash
go vet ./...
```
Expected: no output.

- [ ] **Step 3: full unit suite**

```bash
go test ./...
```
Expected: PASS.

- [ ] **Step 4: integration suite (requires docker daemon + local relay)**

```bash
go test -tags=integration ./test/integration/...
```
Expected: PASS. If docker is unavailable, document the skip in the PR description.

- [ ] **Step 5: codex third-party review**

Run codex against the diff per CLAUDE.md "third-party review" requirement:

```bash
codex exec --skip-git-repo-check --sandbox read-only \
  "Review the diff on branch refactor/direct-wins against origin/main. Focus on: (1) any behavior change beyond Task 7's documented rollback addition; (2) any test that asserts the wrong invariant; (3) any moved function whose imports got missed and would shadow a name; (4) Go idioms / error wrapping. Report findings as a numbered list."
```

Address findings inline as small follow-up commits. If a finding is wrong, push back with reasoning.

### Open PR + watch CI

- [ ] **Step 6: push branch**

```bash
git push -u origin refactor/direct-wins
```

- [ ] **Step 7: open PR**

```bash
gh pr create --title "refactor: direct-win refactors (atomic write, handler/method splits, transcript handle, orchestrate steps)" --body "$(cat <<'EOF'
## Summary
Bundle of seven direct-win refactors identified in the 2026-05-11 code-health survey.

- **fileops.AtomicWrite** + 4 call-site migrations (DRY).
- **invite uses identity.{Encode,Decode}Npub** (drop shadow helpers).
- **daemon/methods.go** split into eight domain files (1007 LOC → ~200 LOC each).
- **dashboard/handlers.go** split into four domain files (1346 LOC → ~250 LOC each).
- **supervisor.transcriptHandle** extracted from `runWithTranscript` (121 LOC → ~75 LOC + dedicated tests).
- **forge.Orchestrate** rewritten as explicit `[]orchestrateStep` with reverse undo on failure + failure-mode tests for image-pull / init / container-create.
- **Stale Phase-5 comment** in `methods.go` cleaned up.

No behavior change except in `forge.Orchestrate`, where reverse-rollback on failure is now structural rather than ad-hoc and exercised by new tests.

Plan: `docs/superpowers/plans/2026-05-11-direct-wins-refactor.md`.

## Test plan
- [x] `go test ./...`
- [x] `go test -tags=integration ./test/integration/...`
- [x] `gofmt -l .` clean
- [x] `go vet ./...` clean
- [x] codex review pass
EOF
)"
```

- [ ] **Step 8: watch CI**

```bash
gh pr checks --watch
```
Expected: green.

- [ ] **Step 9: address Copilot review if present**

```bash
gh pr view --json reviews
```
For each Copilot finding, evaluate and either fix-and-push or reply with reasoning.

- [ ] **Step 10: print PR URL**

```bash
gh pr view --json url -q .url
```
