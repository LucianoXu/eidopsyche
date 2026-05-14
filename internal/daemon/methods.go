package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// Method registry. Reads should go through state.get; mutations have
// their own per-domain verbs but all route through Daemon.Mutate
// (see internal/daemon/mutate.go). The handful of action / utility
// methods that don't fit either pattern (send, invite.redeem,
// subscribe.refresh, lifecycle.run, daemon.exec-replace, card.scan,
// card.parse) stay registered as-is. inbox.tail is a subscription
// primitive, not a read.
func init() {
	// reads (single funnel)
	register("state.get", stateGet)

	// mutations (each routes through Daemon.Mutate internally)
	register("set-label", setOwnLabel)
	register("contact.add", contactAdd)
	register("contact.add-from-card", contactAddFromCard)
	register("contact.remove", contactRemove)
	register("contact.set-label", contactSetLabel)
	register("contact.set-tier", contactSetTier)
	register("relay.add", relayAdd)
	register("relay.remove", relayRemove)
	register("invite.create", inviteCreate)
	register("invite.revoke", inviteRevoke)
	register("config.set", configSet)

	// actions with network/process side effects (not pure state writes)
	register("send", sendMessage)
	register("subscribe.refresh", subscribeRefresh)
	register("invite.redeem", inviteRedeem)
	register("lifecycle.run", lifecycleRunMethod)
	register("daemon.exec-replace", daemonExecReplace)

	// forge orchestration (host-side container lifecycle)
	register("forge.upgrade", forgeUpgrade)

	// parameter-taking reads (state.get's path-only model can't express
	// since/from/to/limit/status filters; kept until state.get gains
	// optional contributor params)
	register("inbox.list", inboxList)
	register("outbox.list", outboxList)
	register("invite.list", inviteList)

	// subscription primitives (not reads)
	register("inbox.tail", inboxTail)

	// utility RPCs that aren't state operations
	register("card.parse", cardParse)
	register("card.scan", cardScan)

	// service polling — typed response shape (dashboard.ServiceStatus,
	// JobStatusSnapshot) doesn't map cleanly through state.get's
	// map[string]any projection yet.
	register("service.status", serviceStatus)
	register("lifecycle.status", lifecycleStatusMethod)

	// agent runtime state — reads /eidos/run/agent-state.json written by
	// the in-container agent loop; missing file yields zero-value map so
	// callers don't have to special-case "agent-loop not yet started".
	register("agent.state", agentStateMethod)
}

// internalErr wraps a Go error into an IPC internal error response.
func internalErr(err error) *ipc.Error {
	return &ipc.Error{Code: ipc.ErrInternal, Message: err.Error()}
}

// resolveTarget converts a user-supplied target string (npub / hex / label)
// to a hex pubkey, returning an *ipc.Error suitable for direct propagation.
func resolveTarget(ctx context.Context, d *Daemon, input string) (string, *ipc.Error) {
	if input == "" {
		return "", &ipc.Error{Code: ipc.ErrInvalidParams, Message: "empty target"}
	}
	if strings.HasPrefix(input, "npub1") {
		hex, err := identity.DecodeNpub(input)
		if err != nil {
			return "", &ipc.Error{Code: ipc.ErrInvalidNpub, Message: err.Error()}
		}
		return hex, nil
	}
	if isHex64(input) {
		return input, nil
	}
	c, err := d.Repo.GetByLabel(ctx, input)
	if err != nil {
		var amb *contacts.AmbiguousLabelError
		if errors.As(err, &amb) {
			npubs := make([]string, 0, len(amb.Pubkeys))
			for _, pk := range amb.Pubkeys {
				if np, e := identity.EncodeNpub(pk); e == nil {
					npubs = append(npubs, np)
				} else {
					npubs = append(npubs, pk)
				}
			}
			return "", &ipc.Error{
				Code:    ipc.ErrLabelAmbiguous,
				Message: fmt.Sprintf("multiple contacts share label %q: %s; use npub or hex instead", input, strings.Join(npubs, ", ")),
			}
		}
		if errors.Is(err, contacts.ErrNotFound) {
			return "", &ipc.Error{
				Code:    ipc.ErrContactNotFound,
				Message: fmt.Sprintf("no contact with label %q (also tried as npub/hex)", input),
			}
		}
		return "", internalErr(err)
	}
	return c.Pubkey, nil
}

// isHex64 returns true if s is exactly 64 lowercase hexadecimal characters.
func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

// annotateInboxLabels fills in row.Label from the contacts store. A
// per-call cache amortises the lookup when the same sender appears in
// many rows. Lookup errors degrade silently to "" so a missing or
// mis-keyed contact never blocks the listing — the surface still
// renders the short-hex fallback for that row.
func annotateInboxLabels(ctx context.Context, d *Daemon, rows []inbox.Message) {
	cache := map[string]string{}
	for i := range rows {
		rows[i].Label = lookupLabel(ctx, d, rows[i].From, cache)
	}
}

// annotateOutboxLabels mirrors annotateInboxLabels for sent rows.
func annotateOutboxLabels(ctx context.Context, d *Daemon, rows []inbox.Sent) {
	cache := map[string]string{}
	for i := range rows {
		rows[i].Label = lookupLabel(ctx, d, rows[i].To, cache)
	}
}

// lookupLabel returns the contact label for pubkey, "" if unknown.
// Results are cached in the supplied map so a noisy thread does not
// trigger one DB read per row.
func lookupLabel(ctx context.Context, d *Daemon, pubkey string, cache map[string]string) string {
	if pubkey == "" || d == nil || d.Repo == nil {
		return ""
	}
	if v, ok := cache[pubkey]; ok {
		return v
	}
	c, err := d.Repo.Get(ctx, pubkey)
	if err != nil || c == nil {
		cache[pubkey] = ""
		return ""
	}
	cache[pubkey] = c.Label
	return c.Label
}

// peerLabel returns a display string for pubkey suitable for log
// entries: "alice (abc1234…)" when a contact label is known,
// "abc1234…" otherwise. A miss in the contacts store degrades silently
// to the short-hex form so logging never errors out.
func (d *Daemon) peerLabel(ctx context.Context, pubkey string) string {
	label := lookupLabel(ctx, d, pubkey, map[string]string{})
	return contacts.FormatPubkeyWithHex(label, pubkey)
}
