# Tier-2 Delivery Acknowledgement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship tier-2 delivery acknowledgement (`type=ack` envelope) end-to-end — wire format, receiver emit, sender record, CLI/dashboard `✓`/`✓✓` surfacing — closing #21 with both the SPEC.md doc and the implementation in one PR.

**Architecture:** Additive `type=ack` in envelope-v1 (no version bump). Receiver emits ack on successful unwrap+persist for chat/command from whitelisted non-self peers. Sender extends `inbox.Sent` with `AckedAt`/`AckEventID`, mutated via append-delta in the existing JSONL-collapse pattern with a small ack-overlay map. CLI and dashboard render `✓` (relay accepted) / `✓✓` (ack received); SSE drives the live transition.

**Tech Stack:** Go 1.22+, Cobra (CLI), htmx + SSE (dashboard), `internal/envelope`, `internal/inbox`, `internal/daemon`, `internal/dashboard`, `cmd/eidos/gate`.

**Spec:** `docs/superpowers/specs/2026-05-09-message-delivery-status-design.md`

---

### Task 0: Worktree + spec commit

**Files:**
- Create: `.claude/wt-tier2-ack/` (worktree on branch `feat/tier-2-ack`)
- Already-present (in worktree): `docs/superpowers/specs/2026-05-09-message-delivery-status-design.md`, `docs/superpowers/plans/2026-05-09-message-delivery-status-implementation.md`

- [ ] **Step 0.1: Create worktree on a new branch from main**

```bash
cd /data/eidopsyche
git fetch origin main
git worktree add -b feat/tier-2-ack .claude/wt-tier2-ack origin/main
```

Expected: `Preparing worktree (new branch 'feat/tier-2-ack')`. Spec/plan files in the parent worktree will be carried across by `git mv` in step 0.2 — the parent worktree is left clean.

- [ ] **Step 0.2: Move untracked spec + plan into the new worktree**

```bash
mv /data/eidopsyche/docs/superpowers/specs/2026-05-09-message-delivery-status-design.md \
   /data/eidopsyche/.claude/wt-tier2-ack/docs/superpowers/specs/
mv /data/eidopsyche/docs/superpowers/plans/2026-05-09-message-delivery-status-implementation.md \
   /data/eidopsyche/.claude/wt-tier2-ack/docs/superpowers/plans/
```

- [ ] **Step 0.3: Commit spec + plan from inside the worktree**

```bash
cd /data/eidopsyche/.claude/wt-tier2-ack
git add docs/superpowers/specs/2026-05-09-message-delivery-status-design.md \
        docs/superpowers/plans/2026-05-09-message-delivery-status-implementation.md
git commit -m "$(cat <<'EOF'
docs(gate): spec + plan for tier-2 delivery acknowledgement (#21)

Spec: docs/superpowers/specs/2026-05-09-message-delivery-status-design.md
Plan: docs/superpowers/plans/2026-05-09-message-delivery-status-implementation.md

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

Expected: clean commit, no other staged changes. All subsequent steps run inside `/data/eidopsyche/.claude/wt-tier2-ack`.

---

### Task 1: envelope ack type — failing tests first

**Files:**
- Modify: `internal/envelope/envelope.go`
- Modify: `internal/envelope/envelope_test.go`

- [ ] **Step 1.1: Add ack tests to `envelope_test.go` (these will fail until Task 2)**

Append to `internal/envelope/envelope_test.go`:

```go
func TestRoundTripAck(t *testing.T) {
	cases := []Envelope{
		{V: 1, Type: TypeAck, Ref: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		{V: 1, Type: TypeAck,
			Ref:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Client: &Client{Name: "eidos", Ver: "0.11.0"}},
	}
	for _, in := range cases {
		s, err := Encode(in)
		if err != nil {
			t.Fatalf("Encode(%+v) err=%v", in, err)
		}
		out, err := Decode(s)
		if err != nil {
			t.Fatalf("Decode(%q) err=%v", s, err)
		}
		if out.Type != TypeAck || out.Ref != in.Ref {
			t.Errorf("round-trip: got %+v, want %+v", out, in)
		}
	}
}

func TestRejectAck(t *testing.T) {
	bad := []string{
		`{"v":1,"type":"ack"}`,                         // missing ref
		`{"v":1,"type":"ack","ref":"short"}`,           // ref too short
		`{"v":1,"type":"ack","ref":"0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF"}`, // uppercase
		`{"v":1,"type":"ack","ref":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdez"}`, // non-hex
		`{"v":1,"type":"ack","ref":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","text":"x"}`,            // chat field forbidden
		`{"v":1,"type":"ack","ref":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","command":{"name":"x","args":{}}}`, // command field forbidden
	}
	for _, c := range bad {
		if _, err := Decode(c); !errors.Is(err, ErrSchemaViolation) {
			t.Errorf("Decode(%s) err=%v, want ErrSchemaViolation", c, err)
		}
	}
}
```

- [ ] **Step 1.2: Run tests, expect FAIL on `TypeAck` undefined**

```bash
go test ./internal/envelope/ -run 'Ack' -v
```

Expected: compile error or undefined `TypeAck` / `Ref` symbol.

- [ ] **Step 1.3: Implement ack in `internal/envelope/envelope.go`**

Replace the type/struct block at lines 12-35 with:

```go
const (
	TypeChat    Type = "chat"
	TypeCommand Type = "command"
	TypeAck     Type = "ack"
)

const SchemaVersion = 1

type Envelope struct {
	V       int      `json:"v"`
	Type    Type     `json:"type"`
	Text    string   `json:"text,omitempty"`
	Command *Command `json:"command,omitempty"`
	Ref     string   `json:"ref,omitempty"`
	Client  *Client  `json:"client,omitempty"`
}

type Command struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type Client struct {
	Name string `json:"name"`
	Ver  string `json:"ver"`
}
```

Extend `Validate` — add this case inside the existing `switch e.Type {` block (between `TypeCommand` and `default:`):

```go
case TypeAck:
	if e.Text != "" || e.Command != nil {
		return ErrSchemaViolation
	}
	if !isLowerHex64(e.Ref) {
		return ErrSchemaViolation
	}
```

Add a helper at end of file:

```go
func isLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}
```

- [ ] **Step 1.4: Run envelope tests, expect PASS**

```bash
go test ./internal/envelope/ -v
```

Expected: all envelope tests including `TestRoundTripAck` and `TestRejectAck` pass.

- [ ] **Step 1.5: Commit**

```bash
git add internal/envelope/envelope.go internal/envelope/envelope_test.go
git commit -m "$(cat <<'EOF'
feat(envelope): add type=ack with hex64 ref field

Additive within v1; old peers continue to soft-reject as schema_violation.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: extend `inbox.Sent` with ack fields

**Files:**
- Modify: `internal/inbox/types.go`

- [ ] **Step 2.1: Add ack fields to `Sent` struct**

Replace the `Sent` struct in `internal/inbox/types.go`:

```go
type Sent struct {
	V           int      `json:"v"`
	EventID     string   `json:"event_id"`
	SelfEventID string   `json:"self_event_id"`
	InnerID     string   `json:"inner_id"`
	To          string   `json:"to"`
	Kind        int      `json:"kind"`
	Content     string   `json:"content"`
	RumorAt     int64    `json:"rumor_at"`
	SentAt      int64    `json:"sent_at"`
	AcceptedBy  []string `json:"accepted_by"`
	Final       bool     `json:"final,omitempty"`
	AckedAt     int64    `json:"acked_at,omitempty"`
	AckEventID  string   `json:"ack_event_id,omitempty"`
}
```

- [ ] **Step 2.2: Build entire workspace to ensure no other consumer breaks**

```bash
go build ./...
```

Expected: clean build. The new fields are zero-valued for existing call sites; `omitempty` JSON tags ensure round-trip safety on legacy rows.

- [ ] **Step 2.3: Commit**

```bash
git add internal/inbox/types.go
git commit -m "$(cat <<'EOF'
feat(inbox): add Sent.AckedAt and Sent.AckEventID

Tier-2 delivery acknowledgement state on outbox rows. Both fields are
omitempty so pre-existing rows round-trip with zero values.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: ListOutbox ack-overlay merge

**Files:**
- Modify: `internal/inbox/outbox.go`
- Modify: `internal/inbox/inbox_test.go`

- [ ] **Step 3.1: Add merge tests (failing)**

Append to `internal/inbox/inbox_test.go`:

```go
func TestListOutboxMergesAckDelta(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	now := time.Now().Unix()
	pre := Sent{V: 1, EventID: "ev1", InnerID: "rumor1", To: "bob", SentAt: now}
	final := Sent{V: 1, EventID: "ev1", InnerID: "rumor1", To: "bob", SentAt: now,
		AcceptedBy: []string{"wss://r"}, Final: true}
	ackDelta := Sent{V: 1, EventID: "ev1", SentAt: now,
		AckedAt: now + 5, AckEventID: "ackwrap1"}

	if err := s.AppendOutbox(pre); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendOutbox(final); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendOutbox(ackDelta); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ListOutbox(nil, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%d, want 1", len(rows))
	}
	got := rows[0]
	if !got.Final || len(got.AcceptedBy) != 1 {
		t.Errorf("non-ack fields not preserved: %+v", got)
	}
	if got.AckedAt != now+5 || got.AckEventID != "ackwrap1" {
		t.Errorf("ack fields not merged: %+v", got)
	}
}

func TestListOutboxAckFirstWins(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	now := time.Now().Unix()
	_ = s.AppendOutbox(Sent{V: 1, EventID: "ev1", InnerID: "rumor1", To: "bob", SentAt: now})
	_ = s.AppendOutbox(Sent{V: 1, EventID: "ev1", SentAt: now, AckedAt: now + 1, AckEventID: "ack-first"})
	_ = s.AppendOutbox(Sent{V: 1, EventID: "ev1", SentAt: now, AckedAt: now + 9, AckEventID: "ack-second"})

	rows, _ := s.ListOutbox(nil, "", 0)
	if len(rows) != 1 || rows[0].AckEventID != "ack-first" {
		t.Errorf("first-ack-wins violated: %+v", rows)
	}
}
```

- [ ] **Step 3.2: Run, expect FAIL (ack fields not preserved through Final-row collapse)**

```bash
go test ./internal/inbox/ -run 'Outbox' -v
```

Expected: `TestListOutboxMergesAckDelta` fails — current row-replace overwrites ack fields when a Final-true row arrives, or rejects an ack delta after a Final row.

- [ ] **Step 3.3: Implement ack-overlay in `ListOutbox`**

In `internal/inbox/outbox.go`, replace the body of `ListOutbox` from the `collapsed := make(...)` line through the `for sc.Scan()` end-loop (lines 27-57) with:

```go
collapsed := make(map[string]Sent)
order := make([]string, 0)
acks := make(map[string]Sent) // EventID → row carrying ack fields; first-ack-wins

for i := len(files) - 1; i >= 0; i-- {
	f := files[i]
	fp, err := os.Open(f)
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(fp)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var o Sent
		if err := json.Unmarshal(sc.Bytes(), &o); err != nil {
			fp.Close()
			return nil, err
		}
		// Existing Final-stickiness collapse (unchanged shape)
		prev, seen := collapsed[o.EventID]
		if !seen {
			order = append(order, o.EventID)
			collapsed[o.EventID] = o
		} else if o.Final || !prev.Final {
			collapsed[o.EventID] = o
		}
		// Ack overlay: first-ack-wins, independent of Final stickiness
		if o.AckedAt != 0 {
			if _, taken := acks[o.EventID]; !taken {
				acks[o.EventID] = o
			}
		}
	}
	fp.Close()
	if err := sc.Err(); err != nil {
		return nil, err
	}
}

// Apply ack overlay
for eid, a := range acks {
	row := collapsed[eid]
	row.AckedAt = a.AckedAt
	row.AckEventID = a.AckEventID
	collapsed[eid] = row
}
```

- [ ] **Step 3.4: Run tests, expect PASS**

```bash
go test ./internal/inbox/ -v
```

Expected: all inbox tests pass, including the two new ack tests.

- [ ] **Step 3.5: Commit**

```bash
git add internal/inbox/outbox.go internal/inbox/inbox_test.go
git commit -m "$(cat <<'EOF'
feat(inbox): merge ack delta rows in ListOutbox

Adds an ack-overlay map applied after the existing Final-stickiness
collapse so ack fields survive regardless of row ordering. First-ack-wins.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: receiver — emit ack after successful chat/command

**Files:**
- Modify: `internal/daemon/daemon.go`
- Modify: `internal/daemon/dispatch_test.go`

- [ ] **Step 4.1: Read existing dispatch path**

Re-read `internal/daemon/daemon.go:388-450` (`dispatchEnvelope`) to understand the chat-success and command-success branches. The ack must be emitted only after successful append (chat) or successful reply send (command) and only when the rumor is from a non-self whitelisted peer.

- [ ] **Step 4.2: Add a daemon helper `emitAck`**

Append to `internal/daemon/daemon.go` (near `sendChatReply`, ~line 485):

```go
// emitAck builds an envelope-v1 type=ack and publishes it as a NIP-17 wrap
// to the given pubkey. Best-effort: no retry, no self-copy, no Sent row.
// In tests, d.testEmitAck takes precedence to avoid network I/O.
func (d *Daemon) emitAck(ctx context.Context, toPubkey string, ref string) {
	if d.testEmitAck != nil {
		d.testEmitAck(ctx, toPubkey, ref)
		return
	}
	env := envelope.Envelope{
		V:    envelope.SchemaVersion,
		Type: envelope.TypeAck,
		Ref:  ref,
	}
	content, err := envelope.Encode(env)
	if err != nil {
		d.Log.Warn("encode ack envelope", "err", err, "ref", ref)
		return
	}
	wrap, _, err := nostr.Wrap(d.Key.PrivateHex, toPubkey, content)
	if err != nil {
		d.Log.Warn("wrap ack envelope", "err", err, "ref", ref)
		return
	}
	urls := d.publishTargets(ctx, toPubkey)
	if len(urls) == 0 {
		d.Log.Debug("emit ack: no relay targets", "to", toPubkey)
		return
	}
	pubCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = d.Pool.Publish(pubCtx, urls, wrap) // best-effort
}
```

Add the test seam near the existing `testSendChatReply` field on `Daemon`:

```go
testEmitAck func(ctx context.Context, toPubkey string, ref string)
```

- [ ] **Step 4.3: Extract `publishTargets` helper from `methods.go`**

In `internal/daemon/methods.go`, locate the block at lines ~363-385 that builds the `targets` map (own_relays + contact relays + fallback). Extract it into a method on `*Daemon`:

```go
// publishTargets returns the union of own_relays, the recipient's known
// relays, and configured fallback relays, deduplicated.
func (d *Daemon) publishTargets(ctx context.Context, recipientPubkey string) []string {
	targets := map[string]struct{}{}
	rows, err := d.DB.QueryContext(ctx, `SELECT relay_url FROM own_relays`)
	if err == nil {
		for rows.Next() {
			var u string
			_ = rows.Scan(&u)
			targets[u] = struct{}{}
		}
		rows.Close()
	}
	if c, err := d.Repo.Get(ctx, recipientPubkey); err == nil {
		for _, u := range c.Relays {
			targets[u] = struct{}{}
		}
	}
	for _, u := range d.Cfg.Publish.FallbackRelays {
		targets[u] = struct{}{}
	}
	urls := make([]string, 0, len(targets))
	for u := range targets {
		urls = append(urls, u)
	}
	return urls
}
```

Replace the original inline block in `methods.go` with `urls := d.publishTargets(ctx, pk)` (or whichever variable name the existing code uses for the recipient pubkey — verify with grep before edit).

- [ ] **Step 4.4: Wire ack emission into the chat path**

In `internal/daemon/daemon.go` `dispatchEnvelope`, inside `case envelope.TypeChat:`, after the existing `d.broadcastInbox(msg)` call, add (still inside the chat case):

```go
if rumor.PubKey != d.Key.PublicHex {
	d.emitAck(ctx, rumor.PubKey, rumor.ID)
}
```

This guarantees ack only fires when (a) inbox.append succeeded (we only reach here after `AppendInbox` returned nil) and (b) sender is not self. The whitelist gate above (lines 406-412) already returned early for non-contact / blocked.

- [ ] **Step 4.5: Wire ack emission into the command path**

Inside `case envelope.TypeCommand:`, **only when both** the rumor is foreign **and** dispatch succeeded — note the existing v1 spec routes commands only from self (`if rumor.PubKey != d.Key.PublicHex { persistSoftReject(...); return }` at line 434). So in v1, command rumors are always self → no ack to emit (self-copy suppression). Skip ack here. Add a comment:

```go
case envelope.TypeCommand:
	// Commands are accepted from self only (cmd path), so by spec we
	// never emit an ack here — self-copies are not acked.
	...
```

(Just a comment update — no behavioral change in this branch.)

- [ ] **Step 4.6: Add receiver-emit tests**

Following the existing pattern in `dispatch_test.go` (inline `d := newTestDaemon(t)`, string literal pubkeys, `d.Repo.Add` for contacts), append:

```go
func TestDispatch_ChatFromContact_EmitsAck(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	from := "bob-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: from, Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}

	var (
		ackTo  string
		ackRef string
		called int
	)
	d.testEmitAck = func(_ context.Context, to, ref string) {
		called++
		ackTo, ackRef = to, ref
	}

	env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "hi"}
	content, _ := envelope.Encode(env)
	rumor := makeRumor(from, content)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev"}, rumor)

	if called != 1 || ackTo != from || ackRef != rumor.ID {
		t.Errorf("ack call: count=%d to=%q ref=%q; want 1, %q, %q", called, ackTo, ackRef, from, rumor.ID)
	}
}

func TestDispatch_SelfCopy_NoAck(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()

	called := 0
	d.testEmitAck = func(context.Context, string, string) { called++ }

	env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "self-note"}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev-self"}, makeRumor(d.Key.PublicHex, content))

	if called != 0 {
		t.Errorf("ack emitted for self-copy: count=%d", called)
	}
}

func TestDispatch_BlockedSender_NoAck(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	from := "mallory-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: from, Tier: contacts.TierBlocked}); err != nil {
		t.Fatal(err)
	}

	called := 0
	d.testEmitAck = func(context.Context, string, string) { called++ }

	env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "rude"}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev-blk"}, makeRumor(from, content))

	if called != 0 {
		t.Errorf("ack emitted for blocked sender: count=%d", called)
	}
}
```

Existing helpers used: `newTestDaemon` (`internal/daemon/testhelpers_test.go:20`) and `makeRumor` (`internal/daemon/dispatch_test.go:18`).

- [ ] **Step 4.7: Run tests, expect PASS**

```bash
go test ./internal/daemon/ -run 'Ack' -v
```

- [ ] **Step 4.8: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/methods.go internal/daemon/dispatch_test.go
git commit -m "$(cat <<'EOF'
feat(gate): emit type=ack envelope on successful chat receive

Receiver emits a NIP-17 wrapped ack envelope back to non-self whitelisted
senders after their chat has been persisted to the inbox. Best-effort:
no retry, no self-copy, no Sent row. Commands (self-only in v1) are not
acked. Tested via testEmitAck seam.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: sender — handle inbound type=ack

**Files:**
- Modify: `internal/daemon/daemon.go`
- Modify: `internal/daemon/dispatch_test.go`

- [ ] **Step 5.1: Add `handleInboundAck` to daemon.go**

Append in `daemon.go` (near `dispatchEnvelope`):

```go
// handleInboundAck consumes a successfully-decoded type=ack envelope.
// Looks up the matching Sent by InnerID == env.Ref, appends a delta row
// with AckedAt/AckEventID populated. First-ack-wins (idempotent on
// re-receipt). Mismatched / unknown refs are dropped with debug-log.
// Never writes to inbox.jsonl, never triggers a wake.
func (d *Daemon) handleInboundAck(ctx context.Context, ev *gnostr.Event, rumor *gnostr.Event, env envelope.Envelope) {
	// Defense-in-depth: env.Ref already validated by envelope.Decode.
	// Whitelist check: ack must come from a known contact (the original
	// recipient of our message). Self-copies of our own acks shouldn't
	// happen — we never emit acks to ourselves — but guard anyway.
	if rumor.PubKey == d.Key.PublicHex {
		return
	}
	c, err := d.Repo.Get(ctx, rumor.PubKey)
	if err != nil || c.Tier == contacts.TierBlocked {
		d.Log.Debug("dropped ack from non-contact / blocked", "from", rumor.PubKey)
		return
	}

	rows, err := d.Box.ListOutbox(nil, "", 0)
	if err != nil {
		d.Log.Warn("handleInboundAck: list outbox", "err", err)
		return
	}
	var match *inbox.Sent
	for i := range rows {
		if rows[i].InnerID == env.Ref {
			match = &rows[i]
			break
		}
	}
	if match == nil {
		d.Log.Debug("ack ref not found in outbox", "ref", env.Ref, "from", rumor.PubKey)
		return
	}
	if match.To != rumor.PubKey {
		d.Log.Warn("ack from non-recipient", "ref", env.Ref, "expected", match.To, "actual", rumor.PubKey)
		return
	}
	if match.AckedAt != 0 {
		// First-ack-wins: idempotent on duplicate ack.
		return
	}

	delta := inbox.Sent{
		V:          1,
		EventID:    match.EventID,
		SentAt:     match.SentAt,
		AckedAt:    time.Now().Unix(),
		AckEventID: ev.ID,
	}
	if err := d.Box.AppendOutbox(delta); err != nil {
		d.Log.Error("append ack delta", "err", err)
		return
	}

	// Push a fresh Sent row to the dashboard so the bubble flips ✓ → ✓✓.
	merged := *match
	merged.AckedAt = delta.AckedAt
	merged.AckEventID = delta.AckEventID
	d.emitDashEvent(dashboard.Event{Kind: "outbox.message", Sent: &merged})
}
```

- [ ] **Step 5.2: Wire the new case into `dispatchEnvelope`**

Inside `dispatchEnvelope`'s switch block, add:

```go
case envelope.TypeAck:
	d.handleInboundAck(ctx, ev, rumor, env)
	return
```

This branch must precede `default:` (if any) and not fall through to chat/command persistence.

- [ ] **Step 5.3: Tests for ack-receive**

Append to `internal/daemon/dispatch_test.go`. The Ref field of an envelope-v1 ack must be 64 lowercase hex (per Task 1.3); the tests use a fixed 64-hex literal `rumorRef` so both pre-row InnerID and ack ref match validation:

```go
const rumorRef = "00000000000000000000000000000000000000000000000000000000aaaaaaaa"

func TestDispatch_InboundAck_MutatesOutbox(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	to := "bob-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: to, Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}
	if err := d.Box.AppendOutbox(inbox.Sent{V: 1, EventID: "evW", InnerID: rumorRef, To: to, SentAt: 100}); err != nil {
		t.Fatal(err)
	}

	env := envelope.Envelope{V: 1, Type: envelope.TypeAck, Ref: rumorRef}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ackwrap1"}, makeRumor(to, content))

	rows, _ := d.Box.ListOutbox(nil, "", 0)
	if len(rows) != 1 || rows[0].AckedAt == 0 || rows[0].AckEventID != "ackwrap1" {
		t.Errorf("ack not recorded: %+v", rows)
	}
}

func TestDispatch_InboundAck_UnknownRef_Drops(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	to := "bob-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: to, Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}

	env := envelope.Envelope{V: 1, Type: envelope.TypeAck, Ref: strings.Repeat("a", 64)}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ackwrap-orphan"}, makeRumor(to, content))

	rows, _ := d.Box.ListOutbox(nil, "", 0)
	if len(rows) != 0 {
		t.Errorf("orphan ack created rows: %+v", rows)
	}
}

func TestDispatch_InboundAck_WrongSender_Rejects(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	bob := "bob-pubkey-hex"
	carol := "carol-pubkey-hex"
	for _, p := range []string{bob, carol} {
		if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: p, Tier: contacts.TierFriend}); err != nil {
			t.Fatal(err)
		}
	}
	_ = d.Box.AppendOutbox(inbox.Sent{V: 1, EventID: "evW", InnerID: rumorRef, To: bob, SentAt: 100})

	env := envelope.Envelope{V: 1, Type: envelope.TypeAck, Ref: rumorRef}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ackwrap"}, makeRumor(carol, content))

	rows, _ := d.Box.ListOutbox(nil, "", 0)
	if len(rows) != 1 || rows[0].AckedAt != 0 {
		t.Errorf("ack from wrong sender was accepted: %+v", rows)
	}
}

func TestDispatch_InboundAck_FirstAckWins(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	to := "bob-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: to, Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}
	_ = d.Box.AppendOutbox(inbox.Sent{V: 1, EventID: "evW", InnerID: rumorRef, To: to, SentAt: 100,
		AckedAt: 999, AckEventID: "ackwrap-original"})

	env := envelope.Envelope{V: 1, Type: envelope.TypeAck, Ref: rumorRef}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ackwrap-second"}, makeRumor(to, content))

	rows, _ := d.Box.ListOutbox(nil, "", 0)
	if rows[0].AckEventID != "ackwrap-original" {
		t.Errorf("first-ack-wins violated: got %s", rows[0].AckEventID)
	}
}
```

Add `"github.com/LucianoXu/eidopsyche/internal/inbox"` to the imports if not already present.

- [ ] **Step 5.4: Run tests, expect PASS**

```bash
go test ./internal/daemon/ -run 'InboundAck' -v
```

- [ ] **Step 5.5: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/dispatch_test.go
git commit -m "$(cat <<'EOF'
feat(gate): consume inbound type=ack envelopes; mutate outbox

Look up Sent by InnerID == ref, append delta row with AckedAt/AckEventID,
emit outbox.message dash event for live SSE flip. First-ack-wins.
Mismatched / orphan / blocked / self acks dropped silently.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: dashboard — emit `outbox.message` on send and broadcast ack-flips

**Files:**
- Modify: `internal/daemon/methods.go`
- Modify: `internal/dashboard/render.go`
- Modify: `internal/dashboard/handlers.go`
- Modify: `internal/dashboard/templates/bubble.html`

The dashboard SSE pipeline already routes `outbox.message` events to `outbox.message:<peer>` channels (`internal/dashboard/sse.go:84-93`), but no daemon site emits them. We wire emission at three sites:

- [ ] **Step 6.1: Emit `outbox.message` after the initial pre-row append in `methods.go`**

Locate `internal/daemon/methods.go:359` (`if err := d.Box.AppendOutbox(pre); err != nil { ... }`). Right after a successful append, add:

```go
preCopy := pre
d.emitDashEvent(dashboard.Event{Kind: "outbox.message", Sent: &preCopy})
```

- [ ] **Step 6.2: Emit `outbox.message` after the final-row append**

Locate `methods.go:404` (`_ = d.Box.AppendOutbox(final)`). Right after, add:

```go
finalCopy := final
d.emitDashEvent(dashboard.Event{Kind: "outbox.message", Sent: &finalCopy})
```

(Task 5 already emits the third site — the ack-delta — inside `handleInboundAck`.)

- [ ] **Step 6.3: Extend `bubbleData` with delivery status**

In `internal/dashboard/render.go`, replace the `bubbleData` struct:

```go
type bubbleData struct {
	Self         bool
	From         string
	Text         string
	At           time.Time
	Malformed    bool
	RejectReason string
	EventID      string
	Status       string // "" | "sent" | "delivered" — only meaningful when Self=true
}
```

- [ ] **Step 6.4: Populate `Status` in `sentToBubble`**

In `internal/dashboard/handlers.go`, replace `sentToBubble`:

```go
func sentToBubble(s inbox.Sent) bubbleData {
	text := s.Content
	if env, err := envelope.Decode(s.Content); err == nil && env.Type == envelope.TypeChat {
		text = env.Text
	}
	status := ""
	switch {
	case s.AckedAt != 0:
		status = "delivered"
	case len(s.AcceptedBy) > 0:
		status = "sent"
	}
	return bubbleData{
		Self:    true,
		From:    "you",
		Text:    text,
		At:      time.Unix(s.SentAt, 0),
		EventID: s.EventID,
		Status:  status,
	}
}
```

- [ ] **Step 6.5: Render `Status` in the bubble template**

Replace `internal/dashboard/templates/bubble.html`:

```html
{{define "bubble"}}<div class="bubble{{if .Self}} self{{end}}{{if .Malformed}} malformed{{end}}" data-event-id="{{.EventID}}">
  <div class="meta">
    {{.From}} · <span title="{{.At.Format "2006-01-02 15:04:05"}}">{{reltime .At}}</span>
    {{if .Malformed}}<span class="reject-chip">{{.RejectReason}}</span>{{end}}
    {{if and .Self (eq .Status "sent")}}<span class="status status-sent" title="accepted by relay">✓</span>{{end}}
    {{if and .Self (eq .Status "delivered")}}<span class="status status-delivered" title="delivered to peer">✓✓</span>{{end}}
  </div>
  <div>{{.Text}}</div>
</div>{{end}}
```

- [ ] **Step 6.6: Render-test the new Status field**

In `internal/dashboard/render_test.go`, add a test confirming the template emits `✓✓` for `Status: "delivered"` and `✓` (single, not double) for `Status: "sent"`. If `render_test.go` already has bubble tests, mirror the existing pattern. Otherwise:

```go
func TestBubbleRendersDelivered(t *testing.T) {
	r, err := NewRendererForTest()
	if err != nil {
		t.Fatal(err)
	}
	html, err := r.Render("bubble", bubbleData{Self: true, From: "you", Text: "hi", Status: "delivered"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "✓✓") {
		t.Errorf("delivered bubble missing ✓✓: %s", html)
	}
	if strings.Contains(html, "✓✓✓") {
		t.Errorf("delivered bubble has triple check: %s", html)
	}
}

func TestBubbleRendersSent(t *testing.T) {
	r, err := NewRendererForTest()
	if err != nil {
		t.Fatal(err)
	}
	html, _ := r.Render("bubble", bubbleData{Self: true, From: "you", Text: "hi", Status: "sent"})
	if !strings.Contains(html, "✓") || strings.Contains(html, "✓✓") {
		t.Errorf("sent bubble should have ✓ and not ✓✓: %s", html)
	}
}
```

If `NewRendererForTest` doesn't exist, use whatever helper the existing render tests use — search `grep -n "func.*Render\|NewRenderer" internal/dashboard/*.go`.

- [ ] **Step 6.7: Run tests**

```bash
go test ./internal/dashboard/ ./internal/daemon/ -v
```

Expected: all pass; no other dashboard test breaks because of the new `Status` field (it has a zero-value-safe default).

- [ ] **Step 6.8: Commit**

```bash
git add internal/daemon/methods.go internal/dashboard/render.go internal/dashboard/handlers.go internal/dashboard/templates/bubble.html internal/dashboard/render_test.go
git commit -m "$(cat <<'EOF'
feat(dashboard): emit outbox.message and render ✓ / ✓✓ status

Wires the missing outbox.message SSE emit at three sites: initial pre-row
append, final-row publish-state append, and ack-delta append (the last
already wired by handleInboundAck). Bubble template renders ✓ for relay
acceptance and ✓✓ for tier-2 ack receipt.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: CLI outbox status column

**Files:**
- Modify: `cmd/eidos/gate/outbox.go`

- [ ] **Step 7.1: Add status formatter and update render loop**

Replace the render loop in `cmd/eidos/gate/outbox.go` (the `for _, m := range resp { ... }` block):

```go
for _, m := range resp {
    ts := time.Unix(int64(m["sent_at"].(float64)), 0)
    status := outboxStatus(m)
    fmt.Printf("%s  %s  -> %.16s  %s\n",
        ts.Format("2006-01-02 15:04:05"), status, m["to"], m["content"])
}
```

Append the helper at end of file:

```go
func outboxStatus(m map[string]any) string {
    if v, ok := m["acked_at"].(float64); ok && v != 0 {
        return "✓✓"
    }
    if accepted, ok := m["accepted_by"].([]any); ok && len(accepted) > 0 {
        return "✓ "
    }
    return "  "
}
```

The two-space "  " fallback keeps the column aligned for any (unexpected) row with no relay acceptance.

- [ ] **Step 7.2: Build to confirm**

```bash
go build ./cmd/eidos
```

Expected: clean build.

- [ ] **Step 7.3: Manual smoke test (skip if no daemon running locally; covered by integration in Task 8)**

```bash
# (only if a local daemon is running with prior outbox rows)
./bin/eidos gate outbox --limit 5
```

Expected: rows render with `✓` or `✓✓` column.

- [ ] **Step 7.4: Commit**

```bash
git add cmd/eidos/gate/outbox.go
git commit -m "$(cat <<'EOF'
feat(cli): outbox shows ✓ / ✓✓ delivery status

Reads acked_at and accepted_by from the outbox.list IPC response and
renders a tier-1 / tier-2 column.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: integration test — A sends to B, A's outbox flips ✓ → ✓✓

**Files:**
- Modify: `test/integration/envelope_test.go` (the existing two-daemon harness uses `bringUp(t, name)` + `addContact(t, a, b)` from `loopback_test.go`)

- [ ] **Step 8.1: Append ack-flow test to the existing envelope integration file**

Append to `test/integration/envelope_test.go`:

```go
func TestEnvelope_AckRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")
	addContact(t, alice, bob)
	addContact(t, bob, alice)

	env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "ping"}
	sendEnvelopeIPC(t, alice, bob.daemon.Key.PublicHex, env)

	// Wait for bob's inbox row (so we know bob unwrapped + persisted),
	// then poll alice's outbox for AckedAt.
	row := waitForInboxFrom(t, bob, alice.daemon.Key.PublicHex, 5*time.Second)
	if row.Malformed {
		t.Fatalf("bob's row malformed: %+v", row)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := alice.daemon.Box.ListOutbox(nil, "", 0)
		if err == nil {
			for _, r := range rows {
				if r.To == bob.daemon.Key.PublicHex && r.AckedAt != 0 {
					if r.AckEventID == "" {
						t.Errorf("AckEventID empty: %+v", r)
					}
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("alice's outbox never received ack from bob within 5s")
}
```

The test reuses `bringUp` / `addContact` / `sendEnvelopeIPC` / `waitForInboxFrom` from `loopback_test.go` and the existing `envelope_test.go`. No new harness code required.

- [ ] **Step 8.3: Run integration tests**

```bash
go test -tags=integration ./test/integration/ -run TierTwoAck -v
```

Expected: PASS within ~1-2s on local relays.

- [ ] **Step 8.4: Commit**

```bash
git add test/integration/
git commit -m "$(cat <<'EOF'
test(gate): integration test for tier-2 ack round-trip

A→B chat with two ephemeral daemons; assert A's outbox row gains
AckedAt and AckEventID once B's daemon emits the ack envelope.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: SPEC.md amendment

**Files:**
- Modify: `SPEC.md`

- [ ] **Step 9.1: Insert the 投递状态分层 bullet**

In `SPEC.md`, locate the bullet `- **异步与实时分离**。` (around line 177). Insert immediately after that bullet (before `- **以点对点（1:1）为基础**。`):

```markdown
- **投递状态分层**。MindGate 区分两层投递语义，UI 不混淆。
  - **Tier 1 — relay 接受**。本地事实：N 个 relay 在发布时返回 OK，记录于 `Sent.AcceptedBy`。CLI/dashboard 渲染为 `✓`。无协议承诺。
  - **Tier 2 — 对端回执**。envelope-v1 中的 `type=ack` 消息：对端 daemon 在成功解开 gift wrap、校验 envelope、持久化到 inbox 后，向原发件方发出 `{v:1, type:"ack", ref:<inner_id>}`。命中后 `Sent.AckedAt` / `Sent.AckEventID` 写入，UI 渲染为 `✓✓`。
  - **不替代项**。NIP-65 / kind:10050 "对方有订阅 inbox" 不等价于"已投递"，不应作为 tier 2 的代理出现在状态界面。
  - **优雅降级**。未升级的旧 daemon 收到 ack 会按 envelope 软拒绝（`schema_violation`），发件方的 `✓✓` 不会出现，仅显示 `✓`。
  - 设计 trace: [#21](https://github.com/LucianoXu/eidopsyche/issues/21)。
```

- [ ] **Step 9.2: Commit**

```bash
git add SPEC.md
git commit -m "$(cat <<'EOF'
docs(spec): name the two tiers of message delivery status (#21)

Adds 投递状态分层 bullet under MindGate 设计选择, distinguishing tier-1
(relay accepted) from tier-2 (peer ack). Forecloses the NIP-65 kind:10050
substitution and notes graceful degradation against pre-ack peers.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 10: envelope-v1 spec amendments

**Files:**
- Modify: `docs/superpowers/specs/2026-05-07-envelope-v1-design.md`

- [ ] **Step 10.1: Update §3 wire format**

In §3 ("Wire format"), find the `Envelope` struct definition or the JSON schema example. Add `Ref string` (with `omitempty`) to the field list and add `type=ack` to the enumeration of types.

- [ ] **Step 10.2: Update §3.1 validation rules**

Add a new bullet under §3.1 alongside the existing chat / command rules:

```markdown
- `type=ack`: `ref` is required, must be exactly 64 lowercase hex characters
  (the inner rumor id of the message being acknowledged). `text` and `command`
  are forbidden. `client` remains optional. Old peers (which know only chat
  and command) reject this case as `ErrSchemaViolation` via the existing
  `default:` branch — graceful degradation requires no code change on their
  side.
```

Also add a one-paragraph "Schema evolution" note at the end of §3.1:

```markdown
**Schema evolution.** New non-breaking types may land additively within a
major version (`v` unchanged); the existing `default: return ErrSchemaViolation`
branch handles unknown types as soft-rejects, so old peers degrade gracefully.
Only breaking changes — field-semantics shifts on existing types, validation
tightening, removed types — require bumping `v`.
```

- [ ] **Step 10.3: Update §10 v2-candidates framing**

Reframe the §10 preamble. Replace whatever currently implies "all new types bump v" with:

```markdown
The items below are candidates that **would** coincide with the next
version bump because they require breaking changes (new mandatory fields,
modified semantics on existing types, or fields whose absence in a v1 peer
would corrupt user-visible behavior). Strictly additive types — like
`type=ack` shipped in `2026-05-09-message-delivery-status-design.md` —
land in v1 directly via the schema-evolution clause in §3.1.
```

Add a "Resolved in v1 additively" subsection at the end of §10:

```markdown
### Resolved in v1 additively

- `type=ack` — tier-2 delivery acknowledgement (`2026-05-09-message-delivery-status-design.md`).
```

- [ ] **Step 10.4: Commit**

```bash
git add docs/superpowers/specs/2026-05-07-envelope-v1-design.md
git commit -m "$(cat <<'EOF'
docs(envelope): clarify additive-types extension contract; record ack

Amends envelope-v1 spec §3, §3.1, §10 to formalize that new non-breaking
types may land within a major version (e.g. type=ack), and only breaking
changes bump v.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 11: local CI parity

**Files:** none (verification only)

- [ ] **Step 11.1: gofmt + go vet**

```bash
gofmt -l . | tee /tmp/gofmt.out
test ! -s /tmp/gofmt.out
go vet ./...
```

Expected: empty `gofmt` output, no vet errors. If `gofmt` reports files, run `gofmt -w .` and re-stage.

- [ ] **Step 11.2: staticcheck**

```bash
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
```

Expected: clean, or only pre-existing warnings unrelated to this branch.

- [ ] **Step 11.3: unit tests**

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 11.4: integration tests**

```bash
go test -tags=integration ./...
```

Expected: PASS. Requires Docker daemon and local Nostr relay — same environment as CI.

- [ ] **Step 11.5: build the binary**

```bash
go build -o bin/eidos ./cmd/eidos
```

Expected: clean build.

If any step fails, fix root cause (do **not** skip hooks or `--no-verify`), re-run, then continue.

---

### Task 12: codex review

**Files:** none (review only)

- [ ] **Step 12.1: Generate the diff for codex**

```bash
git diff origin/main..HEAD > /tmp/tier2-ack.diff
git log --oneline origin/main..HEAD
```

- [ ] **Step 12.2: Invoke codex with the diff**

```bash
codex exec --skip-git-repo-check "$(cat <<'EOF'
Review this branch for:
1. Bugs / logic errors in the ack emit/receive paths.
2. Race conditions around outbox ListOutbox + AppendOutbox.
3. Test coverage gaps (especially edge cases around the merge logic).
4. Adherence to AGENTS.md guidelines (Conventional Commits, error wrapping, single call path).
5. Spec/code drift: design doc says X, code does Y.

Branch: feat/tier-2-ack
Spec: docs/superpowers/specs/2026-05-09-message-delivery-status-design.md
Plan: docs/superpowers/plans/2026-05-09-message-delivery-status-implementation.md

Diff is in /tmp/tier2-ack.diff. Cd into /data/eidopsyche/.claude/wt-tier2-ack
to inspect full files.
EOF
)"
```

If `codex` is not on PATH, follow the project's existing convention (search `grep -rn "codex" docs/ AGENTS.md CLAUDE.md` for the canonical invocation).

- [ ] **Step 12.3: Address findings**

Triage each finding: real bug → new commit fixing it; style nit → fix; false positive → ignore with note in PR description. Re-run Task 11 after fixes.

---

### Task 13: PR

**Files:** none

- [ ] **Step 13.1: Push branch**

```bash
git push -u origin feat/tier-2-ack
```

- [ ] **Step 13.2: Open PR**

```bash
gh pr create --title "feat(gate): tier-2 delivery acknowledgement (#21)" --body "$(cat <<'EOF'
## Summary

- Closes #21. Documents *and* implements both tiers of message-delivery status.
- **Tier 1** (relay accepted, `✓`) was already captured in `Sent.AcceptedBy`; now surfaced in CLI and dashboard.
- **Tier 2** (peer-confirmed receipt, `✓✓`) introduces additive `type=ack` envelope in v1 (no version bump). Receiver emits ack on successful unwrap+persist; sender records it as a `Sent` delta row.

## Spec

- Design: `docs/superpowers/specs/2026-05-09-message-delivery-status-design.md`
- Plan: `docs/superpowers/plans/2026-05-09-message-delivery-status-implementation.md`
- SPEC.md gains the `投递状态分层` bullet under MindGate `设计选择` with cross-link to #21.
- envelope-v1 spec amended (§3, §3.1, §10) to formalize the additive-types extension contract.

## Test plan

- [x] Unit: envelope ack encode/decode/validate
- [x] Unit: ListOutbox ack-overlay merge (Final-row interaction, first-ack-wins)
- [x] Unit: receiver emits ack on chat (gates: self-copy, blocked, malformed)
- [x] Unit: sender consumes ack (idempotent, mismatch rejection, orphan ref drop)
- [x] Unit: dashboard bubble renders ✓ vs ✓✓
- [x] Integration: A → B chat → A's outbox flips ✓ → ✓✓
- [x] gofmt / vet / staticcheck / unit / integration all clean

## Migration

- Old peers receiving acks soft-reject as `schema_violation` — they show a malformed inbox row only via diagnostic flags; default inbox view is unaffected.
- Old peers don't emit acks → senders show `✓` indefinitely until peer upgrades. Acceptable; documented in SPEC.md.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 13.3: Watch CI**

```bash
gh pr checks --watch
```

Expected: all checks green.

- [ ] **Step 13.4: Address Copilot auto-review (if assigned)**

```bash
gh pr view --comments
```

Triage each Copilot comment as in Step 12.3.

- [ ] **Step 13.5: Print PR URL for the user**

```bash
gh pr view --json url --jq .url
```
