package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/card"
	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// ContactAddFromCardParams is the parameter shape for the
// "contact.add-from-card" IPC method. URI is a mindgate:// card; if
// LabelOverride is empty the embedded card label is used.
type ContactAddFromCardParams struct {
	URI           string `json:"uri"`
	LabelOverride string `json:"label_override"`
}

// contactAddFromCard parses a card URI and admits its npub as a contact.
// If the pubkey is already known the row is upserted: label is updated
// (override > embedded), the card's relay hint is appended if new, and
// the existing tier is preserved. Strict-add semantics live in
// contact.add; this method is for the dashboard's "scan & add" flow,
// where re-presenting the same card refreshes contact metadata
// instead of failing with CONTACT_EXISTS.
func contactAddFromCard(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p ContactAddFromCardParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	c, err := card.Parse(strings.TrimSpace(p.URI))
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrCardInvalid, Message: err.Error()}
	}
	pk, err := identity.DecodeNpub(c.Npub)
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidNpub, Message: err.Error()}
	}
	label := strings.TrimSpace(p.LabelOverride)
	if label == "" {
		label = strings.TrimSpace(c.Label)
	}
	if label == "" {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams,
			Message: "card has no label and none was provided"}
	}

	// Upsert path.
	if existing, err := d.Repo.Get(ctx, pk); err == nil && existing != nil {
		if err := d.Repo.SetLabel(ctx, pk, label); err != nil {
			return nil, internalErr(err)
		}
		relayAdded := false
		if c.Relay != "" && !relayListContains(existing.Relays, c.Relay) {
			if err := d.Repo.AddRelay(ctx, pk, c.Relay); err != nil {
				return nil, internalErr(err)
			}
			relayAdded = true
		}
		d.emitDashEvent(dashboard.Event{Kind: "contact.relabeled"})
		if relayAdded {
			d.Refresh()
		}
		saved, _ := d.Repo.Get(ctx, pk)
		return saved, nil
	}

	// New-contact path. Skip the relay hint if empty so subscriptionURLs
	// doesn't end up with an empty URL (which poisons relay health with
	// dial failures).
	contact := contacts.Contact{
		Pubkey: pk,
		Label:  label,
		Tier:   contacts.TierFriend,
	}
	if c.Relay != "" {
		contact.Relays = []string{c.Relay}
	}
	if err := d.Repo.Add(ctx, contact); err != nil {
		return nil, internalErr(err)
	}
	d.emitDashEvent(dashboard.Event{Kind: "contact.added"})
	if c.Relay != "" {
		d.Refresh()
	}
	saved, _ := d.Repo.Get(ctx, pk)
	return saved, nil
}

// contactAdd adds a new contact from an npub + optional relays/label/tier.
// Routes through daemon.Mutate so the change emits state.changed for
// the contacts subtree. The Refresh() call (which re-evaluates which
// relays the subscriber loop watches) stays outside Mutate so a slow
// network refresh doesn't hold the lock; the write itself is fast.
func contactAdd(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		Npub   string   `json:"npub"`
		Relays []string `json:"relays"`
		Label  string   `json:"label"`
		Tier   string   `json:"tier"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	pk, err := identity.DecodeNpub(p.Npub)
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidNpub, Message: err.Error()}
	}
	c := contacts.Contact{Pubkey: pk, Label: p.Label, Tier: contacts.Tier(p.Tier), Relays: p.Relays}
	merr := d.Mutate(ctx, "contacts."+pk, config.BothCtx,
		func() (any, any, error) {
			if err := d.Repo.Add(ctx, c); err != nil {
				if errors.Is(err, contacts.ErrExists) {
					return nil, nil, &ipc.Error{Code: ipc.ErrContactExists, Message: pk}
				}
				return nil, nil, err
			}
			return nil, c, nil
		})
	if merr != nil {
		return nil, asIPCError(merr)
	}
	d.Refresh()
	return map[string]bool{"ok": true}, nil
}

// contactList returns all contacts in the repo as the typed slice
// the contacts package owns. CLI and dashboard both decode into the
// same struct; npub-form rendering is a render-time concern.
func contactList(ctx context.Context, d *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	all, err := d.Repo.List(ctx)
	if err != nil {
		return nil, internalErr(err)
	}
	return all, nil
}

// ContactSetTierParams is the JSON-stable parameter shape for
// "contact.set-tier". Target accepts npub / hex / label (resolved via
// resolveTarget); Tier must be one of the four constants in
// internal/contacts.
type ContactSetTierParams struct {
	Target string `json:"target"`
	Tier   string `json:"tier"`
}

// contactGet looks up a single contact and returns the full record.
// Target accepts npub / hex / label; the same resolveTarget rules as
// every other target-bearing method apply.
func contactGet(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		Target string `json:"target"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	pk, ipcErr := resolveTarget(ctx, d, p.Target)
	if ipcErr != nil {
		return nil, ipcErr
	}
	c, err := d.Repo.Get(ctx, pk)
	if err != nil {
		if errors.Is(err, contacts.ErrNotFound) {
			return nil, &ipc.Error{Code: ipc.ErrContactNotFound, Message: pk}
		}
		return nil, internalErr(err)
	}
	return c, nil
}

// contactSetTier updates the tier of an existing contact. Validation
// lives in contacts.Repo.SetTier; invalid tier strings come back as
// INVALID_PARAMS, missing contacts as CONTACT_NOT_FOUND. Routes
// through daemon.Mutate; emits state.changed.
func contactSetTier(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p ContactSetTierParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	pk, ipcErr := resolveTarget(ctx, d, p.Target)
	if ipcErr != nil {
		return nil, ipcErr
	}
	merr := d.Mutate(ctx, "contacts."+pk+".tier", config.BothCtx,
		func() (any, any, error) {
			if err := d.Repo.SetTier(ctx, pk, contacts.Tier(p.Tier)); err != nil {
				if errors.Is(err, contacts.ErrNotFound) {
					return nil, nil, &ipc.Error{Code: ipc.ErrContactNotFound, Message: pk}
				}
				return nil, nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
			}
			return nil, p.Tier, nil
		})
	if merr != nil {
		return nil, asIPCError(merr)
	}
	d.emitDashEvent(dashboard.Event{Kind: "contact.tier-changed"})
	return map[string]string{"pubkey": pk, "tier": p.Tier}, nil
}

// contactRemove removes a contact by npub, hex pubkey, or label.
// Routes through daemon.Mutate; emits state.changed for the contacts
// subtree. Refresh() runs after the lock releases so the subscriber
// loop re-evaluates without blocking other mutations.
func contactRemove(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct{ Npub string }
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	pk, ipcErr := resolveTarget(ctx, d, p.Npub)
	if ipcErr != nil {
		return nil, ipcErr
	}
	merr := d.Mutate(ctx, "contacts."+pk, config.BothCtx,
		func() (any, any, error) {
			if err := d.Repo.Remove(ctx, pk); err != nil {
				if errors.Is(err, contacts.ErrNotFound) {
					return nil, nil, &ipc.Error{Code: ipc.ErrContactNotFound, Message: pk}
				}
				return nil, nil, err
			}
			return pk, nil, nil
		})
	if merr != nil {
		return nil, asIPCError(merr)
	}
	d.Refresh()
	return map[string]bool{"ok": true}, nil
}

// contactSetLabel renames an existing contact. Target accepts npub, hex
// pubkey, or current label (subject to the usual ambiguity rules).
// Routes through daemon.Mutate; emits state.changed for the contact.
func contactSetLabel(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
	var p struct {
		Target string
		Label  string
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
	}
	label := strings.TrimSpace(p.Label)
	if label == "" {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "label must not be empty"}
	}
	pk, ipcErr := resolveTarget(ctx, d, p.Target)
	if ipcErr != nil {
		return nil, ipcErr
	}
	merr := d.Mutate(ctx, "contacts."+pk+".label", config.BothCtx,
		func() (any, any, error) {
			if err := d.Repo.SetLabel(ctx, pk, label); err != nil {
				if errors.Is(err, contacts.ErrNotFound) {
					return nil, nil, &ipc.Error{Code: ipc.ErrContactNotFound, Message: pk}
				}
				return nil, nil, err
			}
			return nil, label, nil
		})
	if merr != nil {
		return nil, asIPCError(merr)
	}
	return map[string]string{"pubkey": pk, "label": label}, nil
}
