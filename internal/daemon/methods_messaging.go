package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
	"github.com/LucianoXu/eidopsyche/internal/nostr"
)

// formatNoRelaysError builds the NO_RELAYS_REACHABLE IPC error returned
// when every relay rejects (or fails to deliver) a publish. Each failing
// relay's reason is included so callers can distinguish "TCP/WS dead"
// from "relay returned OK false: blocked: spam" — without this, the
// mind-form sees a fixed "publish failed on all N relays" string and
// retries blindly, treating NIP-20 rejections as transient outages.
func formatNoRelaysError(results []nostr.PublishResult, urlCount int) *ipc.Error {
	msg := fmt.Sprintf("publish failed on all %d relays", urlCount)
	parts := make([]string, 0, len(results))
	for _, r := range results {
		if r.OK {
			continue
		}
		reason := r.Reason
		if reason == "" {
			reason = "no reason reported"
		}
		parts = append(parts, fmt.Sprintf("%s: %s", r.Relay, reason))
	}
	if len(parts) > 0 {
		msg = msg + "; " + strings.Join(parts, "; ")
	}
	return &ipc.Error{Code: ipc.ErrNoRelaysReachable, Message: msg}
}

// SendParams is the JSON-stable parameter shape for the "send" IPC method.
// Callers (CLI ipc.Client.Call, dashboard adapter via *Daemon.Call, future
// MCP server) build this struct rather than ad-hoc maps so the schema is
// explicit at every surface.
type SendParams struct {
	To       string             `json:"to"`
	Envelope *envelope.Envelope `json:"envelope"`
}

// SendResult is the JSON-stable result shape for the "send" IPC method.
type SendResult struct {
	EventID    string   `json:"event_id"`
	AcceptedBy []string `json:"accepted_by"`
}

// sendMessage NIP-17 gift-wraps an envelope-v1 payload and publishes it to
// the recipient's relays plus our own. It also publishes a self-copy for
// archive purposes.
func sendMessage(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p SendParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	if p.Envelope == nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "envelope is required"}
	}
	content, err := envelope.Encode(*p.Envelope)
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}

	pk, ipcErr := resolveTarget(ctx, d, p.To)
	if ipcErr != nil {
		return nil, ipcErr
	}
	c, err := d.Repo.Get(ctx, pk)
	if err != nil {
		// Self-loopback: sending to own pubkey is allowed even when not in
		// contacts. The dispatcher's authority rule (sender==self) handles
		// commands; chat-to-self lands in the operator's own inbox via the
		// self-copy publish.
		if pk != d.Key.PublicHex {
			return nil, &ipc.Error{Code: ipc.ErrContactNotFound, Message: p.To}
		}
		c = &contacts.Contact{Pubkey: pk}
	}

	wrapBob, rumorID, err := nostr.Wrap(d.Key.PrivateHex, pk, content)
	if err != nil {
		return nil, internalErr(err)
	}
	wrapSelf, _, err := nostr.Wrap(d.Key.PrivateHex, d.Key.PublicHex, content)
	if err != nil {
		return nil, internalErr(err)
	}
	d.recordSelfWrap(wrapSelf.ID)

	now := time.Now().Unix()
	pre := inbox.Sent{
		EventID:     wrapBob.ID,
		SelfEventID: wrapSelf.ID,
		InnerID:     rumorID,
		To:          pk,
		Kind:        14,
		Content:     content,
		RumorAt:     now,
		SentAt:      now,
		AcceptedBy:  nil,
	}
	if err := d.Box.AppendOutbox(pre); err != nil {
		return nil, internalErr(err)
	}
	// Note: we deliberately do NOT emit `outbox.message` here. The
	// dashboard's POST /thread/{pubkey}/send response delivers the
	// initial bubble inline (htmx swaps it `beforeend`). Emitting via
	// SSE would produce a duplicate bubble in the same thread view.
	// Tier-2 status updates (✓ → ✓✓) are delivered via OOB swap from
	// handleInboundAck; see internal/dashboard/sse.go.

	urls, err := d.publishTargets(ctx, c.Relays)
	if err != nil {
		return nil, internalErr(err)
	}

	publishCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resBob := d.Pool.Publish(publishCtx, urls, wrapBob)
	// Record publish-health ONLY for the user-visible recipient publish.
	// The self-copy below is best-effort archive; letting it touch
	// RelayHealth would let a self-copy success mask a real recipient
	// failure on the same relay (codex review on PR for this fix).
	d.recordPublishHealth(resBob)
	resSelf := d.Pool.Publish(publishCtx, urls, wrapSelf)
	_ = resSelf // self-copy publish is best-effort

	accepted := []string{}
	for _, r := range resBob {
		if r.OK {
			accepted = append(accepted, r.Relay)
		}
	}
	if len(accepted) == 0 {
		return nil, formatNoRelaysError(resBob, len(urls))
	}
	final := pre
	final.AcceptedBy = accepted
	final.Final = true
	d.finalizeOutboxOrLog(ctx, final, wrapBob.ID, pk)
	// No SSE emit here either — the POST /send response already rendered
	// the ✓ bubble (deps.Send only returns nil when at least one relay
	// accepted, so the dashboard handler can safely set Status="sent").

	return SendResult{
		EventID:    wrapBob.ID,
		AcceptedBy: accepted,
	}, nil
}

// finalizeOutboxOrLog writes the publish-finalization outbox row and logs
// any error rather than failing the RPC. Publish has already succeeded by
// the time we reach this point, so failing the RPC would mislead callers
// into thinking the network publish itself had failed. A local-write error
// here usually means disk full / permission / filesystem trouble and is
// what causes the "stuck in pending" UI symptom — the log breadcrumb
// gives operators something to chase. Prior code silently discarded this
// error (`_ = d.Box.AppendOutbox(final)`); see codex review 2026-05-10.
func (d *Daemon) finalizeOutboxOrLog(ctx context.Context, sent inbox.Sent, eventID, to string) {
	if err := d.Box.AppendOutbox(sent); err != nil {
		d.Log.ErrorContext(ctx, "send: finalize outbox write failed after publish",
			"err", err.Error(), "event_id", eventID, "to", to)
	}
}

// inboxList returns inbox messages filtered by since/from/limit/sender.
//
// The `sender` field selects which classes of rows survive the contact-
// graph filter:
//   - "known"   (default): exclude rows whose sender has no non-blocked
//     contact row. Self-chats survive.
//   - "unknown":           inverse — return only rows that would be
//     filtered out by "known". Useful for the
//     operator's "pending" inbox tab.
//   - "all":               no contact-graph filter.
//
// When `from` is set, `sender` is ignored: a specific-pubkey query is
// the most specific filter possible.
func inboxList(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		Since  *int64 `json:"since"`
		From   string `json:"from"`
		Limit  int    `json:"limit"`
		Sender string `json:"sender"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
		}
	}
	var sincePtr *time.Time
	if p.Since != nil {
		t := time.Unix(*p.Since, 0)
		sincePtr = &t
	}
	if p.From != "" {
		hex, ipcErr := resolveTarget(ctx, d, p.From)
		if ipcErr != nil {
			return nil, ipcErr
		}
		p.From = hex
	}
	keep, ipcErr := buildSenderKeep(ctx, d, p.From, p.Sender)
	if ipcErr != nil {
		return nil, ipcErr
	}
	var out []inbox.Message
	var err error
	if keep != nil {
		out, err = d.Box.ListInbox(sincePtr, p.From, p.Limit, keep)
	} else {
		out, err = d.Box.ListInbox(sincePtr, p.From, p.Limit)
	}
	if err != nil {
		return nil, internalErr(err)
	}
	annotateInboxRows(ctx, d, out)
	return out, nil
}

// inboxTail subscribes the connection to live inbox push events.
//
// Optional `sender` param mirrors inbox.list: "known" (default),
// "unknown", or "all". The filter is applied per-message at broadcast
// time so a CLI tail asking for known senders does not see strangers,
// and a dashboard pending-pane subscribing with sender=unknown does
// not see the operator's normal traffic.
func inboxTail(_ context.Context, d *Daemon, conn *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		Sender string `json:"sender"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
		}
	}
	filter, ipcErr := parseSenderEnum(p.Sender)
	if ipcErr != nil {
		return nil, ipcErr
	}
	d.addSubscriberWithFilter(conn, filter)
	return map[string]bool{"subscribed": true}, nil
}

// senderFilter is the parsed form of the "sender" enum on inbox.list /
// inbox.tail.
type senderFilter int

const (
	senderKnown senderFilter = iota // default: hide rows where IsPending == true
	senderUnknown
	senderAll
)

// parseSenderEnum normalises the JSON string into a senderFilter. Empty
// string defaults to senderKnown so existing callers (older CLI, scripts)
// keep today's "default to known" behaviour.
func parseSenderEnum(s string) (senderFilter, *ipc.Error) {
	switch s {
	case "", "known":
		return senderKnown, nil
	case "unknown":
		return senderUnknown, nil
	case "all":
		return senderAll, nil
	default:
		return 0, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "sender must be one of \"known\", \"unknown\", \"all\""}
	}
}

// buildSenderKeep returns a Keep predicate for ListInbox based on the
// sender enum, or nil when no filter is needed (sender=all, OR an
// explicit from= filter that supersedes sender). The predicate uses a
// per-call contact-lookup cache so a noisy page does not hit the DB
// once per row.
func buildSenderKeep(ctx context.Context, d *Daemon, from, sender string) (func(inbox.Message) bool, *ipc.Error) {
	filter, ipcErr := parseSenderEnum(sender)
	if ipcErr != nil {
		return nil, ipcErr
	}
	// Specific-pubkey query is its own filter — sender is ignored.
	if from != "" {
		return nil, nil
	}
	if filter == senderAll {
		return nil, nil
	}
	var selfHex string
	if d != nil && d.Key != nil {
		selfHex = d.Key.PublicHex
	}
	cache := map[string]bool{}
	pending := func(pk string) bool {
		if v, ok := cache[pk]; ok {
			return v
		}
		v := contacts.IsPending(ctx, d.Repo, pk, selfHex)
		cache[pk] = v
		return v
	}
	switch filter {
	case senderKnown:
		return func(m inbox.Message) bool { return !pending(m.From) }, nil
	case senderUnknown:
		return func(m inbox.Message) bool { return pending(m.From) }, nil
	}
	return nil, nil
}

// outboxList returns sent messages filtered by since/to/limit.
func outboxList(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		Since *int64 `json:"since"`
		To    string `json:"to"`
		Limit int    `json:"limit"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
		}
	}
	var sincePtr *time.Time
	if p.Since != nil {
		t := time.Unix(*p.Since, 0)
		sincePtr = &t
	}
	if p.To != "" {
		hex, ipcErr := resolveTarget(ctx, d, p.To)
		if ipcErr != nil {
			return nil, ipcErr
		}
		p.To = hex
	}
	out, err := d.Box.ListOutbox(sincePtr, p.To, p.Limit)
	if err != nil {
		return nil, internalErr(err)
	}
	annotateOutboxLabels(ctx, d, out)
	return out, nil
}
