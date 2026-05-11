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

func init() {
	register("whoami", whoami)
	register("set-label", setOwnLabel)
	register("card.export", cardExport)
	register("card.parse", cardParse)
	register("contact.add", contactAdd)
	register("contact.list", contactList)
	register("contact.remove", contactRemove)
	register("contact.set-label", contactSetLabel)
	register("relay.list", relayList)
	register("relay.add", relayAdd)
	register("relay.remove", relayRemove)
	register("relays.health", relaysHealth)
	register("send", sendMessage)
	register("inbox.list", inboxList)
	register("inbox.tail", inboxTail)
	register("outbox.list", outboxList)
	register("version", versionMethod)
	register("subscribe.refresh", subscribeRefresh)
	register("invite.create", inviteCreate)
	register("invite.list", inviteList)
	register("invite.revoke", inviteRevoke)
	register("invite.redeem", inviteRedeem)
	register("config.get", configGet)
	register("config.set", configSet)
	register("contact.get", contactGet)
	register("contact.set-tier", contactSetTier)
	register("card.scan", cardScan)
	register("service.status", serviceStatus)
	register("lifecycle.run", lifecycleRunMethod)
	register("lifecycle.status", lifecycleStatusMethod)
	register("contact.add-from-card", contactAddFromCard)
	register("daemon.exec-replace", daemonExecReplace)
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
