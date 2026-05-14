package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
	"github.com/LucianoXu/eidopsyche/internal/version"
)

// This file registers the daemon-core state contributors. Each
// contributor exposes one domain's read view at a single mount path:
//
//	identity → npub, hex, label, home_relays
//	config   → Config struct (heartbeat, dashboard, mindform, …)
//	contacts → map keyed by hex pubkey
//	relays   → map keyed by url (with health field merged in)
//	inbox    → recent + counts
//	outbox   → recent
//	invites  → list
//	service  → daemon socket / started_at / version / lifecycle status
//	card     → mounted under identity.card (Phase B drops card.export)
//
// Container-only contributors (lifecycle.wakes / session / dream /
// container.*) land in internal/lifecycle in Phase D — those are not
// registered by the host daemon.
//
// Each contributor delegates to the same per-domain data layer
// (Repo.List, Box.ListInbox, etc.) that the legacy IPC reads use, so
// data sources are unified, but the *shape* state.get returns is
// chosen to be Tree-walkable: contacts and relays land as maps keyed
// by hex pubkey / url for indexed access; relays.* entries merge the
// health field that the legacy relays.health surfaced separately;
// identity exposes a card subtree that's a thin URI projection rather
// than the full TOML card body card.export ships. The legacy IPC
// methods stay registered for backward-compat callers; a cleanup PR
// migrates them and removes the duplicates.

func (d *Daemon) registerCoreStateContributors() {
	d.RegisterStateContributor(identityContrib{d: d})
	d.RegisterStateContributor(configContrib{d: d})
	d.RegisterStateContributor(contactsContrib{d: d})
	d.RegisterStateContributor(relaysContrib{d: d})
	d.RegisterStateContributor(inboxContrib{d: d})
	d.RegisterStateContributor(outboxContrib{d: d})
	d.RegisterStateContributor(invitesContrib{d: d})
	d.RegisterStateContributor(serviceContrib{d: d})
	// forge.workspaces is container-side only: mind-forms inspect their own
	// bind mounts. Host operators read the desired/actual diff through the
	// forge.workspace.list IPC method instead.
	if d.Context&config.ContainerCtx != 0 {
		d.RegisterStateContributor(forgeWorkspacesContrib{d: d})
	}
}

// ── identity ────────────────────────────────────────────────────────

type identityContrib struct{ d *Daemon }

func (c identityContrib) Path() string { return "identity" }
func (c identityContrib) Snapshot(ctx context.Context) (any, error) {
	rows, err := c.d.DB.QueryContext(ctx, `SELECT relay_url, role FROM own_relays`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var relays []map[string]string
	for rows.Next() {
		var u, r string
		if err := rows.Scan(&u, &r); err != nil {
			return nil, err
		}
		relays = append(relays, map[string]string{"url": u, "role": r})
	}
	label, _ := c.d.DB.GetMeta(ctx, "label")
	// Build the TOML card body so state.get identity.card works (the
	// legacy card.export method returns just the URI today, but the
	// card subtree is the home for that shape going forward).
	uri := cardExportURI(c.d)
	return map[string]any{
		"pubkey":      c.d.Key.PublicHex,
		"npub":        c.d.Key.Npub,
		"label":       label,
		"home_relays": relays,
		"card":        map[string]any{"uri": uri},
	}, nil
}

// cardExportURI mirrors methods_card.go's logic without going through
// the IPC layer. Kept inline so identityContrib doesn't reach back
// across the IPC handler boundary.
func cardExportURI(d *Daemon) string {
	rows, err := d.DB.QueryContext(context.Background(), `SELECT relay_url FROM own_relays`)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var relays []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err == nil {
			relays = append(relays, u)
		}
	}
	uri := "mindgate://" + d.Key.Npub
	if len(relays) > 0 {
		uri += "@" + relays[0]
	}
	return uri
}

// ── config ──────────────────────────────────────────────────────────

type configContrib struct{ d *Daemon }

func (c configContrib) Path() string { return "config" }
func (c configContrib) Snapshot(_ context.Context) (any, error) {
	cfg, err := config.Load(filepath.Join(c.d.StateDir, "config.toml"))
	if err != nil {
		// Treat missing config.toml as "use defaults" — the same policy
		// the supervisor uses at startup. A genuine I/O error on a
		// present-but-unreadable file still surfaces via config.Load.
		if os.IsNotExist(err) {
			return config.Defaults(), nil
		}
		return nil, err
	}
	return cfg, nil
}

// ── contacts ────────────────────────────────────────────────────────

type contactsContrib struct{ d *Daemon }

func (c contactsContrib) Path() string { return "contacts" }
func (c contactsContrib) Snapshot(ctx context.Context) (any, error) {
	list, err := c.d.Repo.List(ctx)
	if err != nil {
		return nil, err
	}
	// state.get contacts returns the full list; state.get contacts.<pk>
	// returns one entry. Mount as a map keyed by hex pubkey so the
	// tree walker handles the indexed access automatically.
	out := map[string]any{}
	for _, k := range list {
		out[k.Pubkey] = serializeContact(*k)
	}
	return out, nil
}

func serializeContact(k contacts.Contact) map[string]any {
	return map[string]any{
		"pubkey":     k.Pubkey,
		"label":      k.Label,
		"tier":       k.Tier,
		"notes":      k.Notes,
		"relays":     k.Relays,
		"created_at": k.CreatedAt.Unix(),
		"updated_at": k.UpdatedAt.Unix(),
	}
}

// ── relays ──────────────────────────────────────────────────────────

type relaysContrib struct{ d *Daemon }

func (c relaysContrib) Path() string { return "relays" }
func (c relaysContrib) Snapshot(ctx context.Context) (any, error) {
	// Use ListOwnRelays so the snapshot carries AddedAt (the legacy
	// relay.list method returned this field; preserve it so callers
	// migrating from relay.list don't lose data).
	rows, err := c.d.ListOwnRelays(ctx)
	if err != nil {
		return nil, err
	}
	healthByURL := map[string]RelayHealth{}
	for _, h := range c.d.relayHealth.snapshot() {
		healthByURL[h.URL] = h
	}
	relays := map[string]any{}
	for _, r := range rows {
		h := healthByURL[r.URL]
		relays[r.URL] = map[string]any{
			"url":              r.URL,
			"role":             r.Role,
			"added_at":         r.AddedAt,
			"state":            h.State,
			"last_error":       h.LastError,
			"last_event_at":    h.LastEventAt,
			"last_publish_ok":  h.LastPublishOk,
			"last_publish_err": h.LastPublishErr,
			"last_publish_at":  h.LastPublishAt,
		}
	}
	// Surface URLs that exist only in relayHealth (contact relays we
	// subscribed to, configured fallback relays, invite-issuer relays —
	// anywhere recordPublishHealth has fired without a matching
	// own_relays row). Without this, a user-visible publish to a
	// contact's relay can be rejected and recorded in relayHealth, but
	// the rejection never surfaces in state.get relays because the
	// loop above only iterates own_relays.
	for url, h := range healthByURL {
		if _, dup := relays[url]; dup {
			continue
		}
		relays[url] = map[string]any{
			"url":              url,
			"role":             h.Role, // may be "" for publish-only targets
			"added_at":         int64(0),
			"state":            h.State,
			"last_error":       h.LastError,
			"last_event_at":    h.LastEventAt,
			"last_publish_ok":  h.LastPublishOk,
			"last_publish_err": h.LastPublishErr,
			"last_publish_at":  h.LastPublishAt,
		}
	}
	return relays, nil
}

// ── inbox ───────────────────────────────────────────────────────────

type inboxContrib struct{ d *Daemon }

func (c inboxContrib) Path() string { return "inbox" }
func (c inboxContrib) Snapshot(ctx context.Context) (any, error) {
	rows, err := c.d.Box.ListInbox(nil, "", 100)
	if err != nil {
		return nil, err
	}
	annotateInboxLabels(ctx, c.d, rows)
	return map[string]any{
		"recent": rows,
		"count":  len(rows),
	}, nil
}

// ── outbox ──────────────────────────────────────────────────────────

type outboxContrib struct{ d *Daemon }

func (c outboxContrib) Path() string { return "outbox" }
func (c outboxContrib) Snapshot(ctx context.Context) (any, error) {
	rows, err := c.d.Box.ListOutbox(nil, "", 100)
	if err != nil {
		return nil, err
	}
	annotateOutboxLabels(ctx, c.d, rows)
	return map[string]any{
		"recent": rows,
		"count":  len(rows),
	}, nil
}

// ── invites ─────────────────────────────────────────────────────────

type invitesContrib struct{ d *Daemon }

func (c invitesContrib) Path() string { return "invites" }
func (c invitesContrib) Snapshot(ctx context.Context) (any, error) {
	if c.d.Invites == nil {
		return []any{}, nil
	}
	// Pass empty status filter to list everything (matches invite.list's
	// no-filter default).
	list, err := c.d.Invites.List(ctx, "")
	if err != nil {
		return nil, err
	}
	return list, nil
}

// ── service ─────────────────────────────────────────────────────────

type serviceContrib struct{ d *Daemon }

func (c serviceContrib) Path() string { return "service" }
func (c serviceContrib) Snapshot(_ context.Context) (any, error) {
	// version subtree must be map[string]any so state.Tree's walker
	// (which understands map[string]any / structs, not map[string]string)
	// can resolve dotted access like `state.get service.version.version`.
	return map[string]any{
		"socket":     filepath.Join(c.d.StateDir, c.d.Cfg.Daemon.Socket),
		"started_at": c.d.startedAt.Unix(),
		"version": map[string]any{
			"version":    version.Version,
			"commit":     version.Commit,
			"build_date": version.BuildDate,
		},
	}, nil
}

// Sanity check: state.get on every contributor path returns non-error
// for a freshly constructed daemon. Run as part of the unit test
// suite, not at runtime.
func sanityCheckContributors(ctx context.Context, d *Daemon) error {
	for _, path := range []string{"identity", "config", "contacts", "relays", "inbox", "outbox", "invites", "service"} {
		if _, err := d.stateTree.Snapshot(ctx, path); err != nil {
			return fmt.Errorf("state.get %s: %w", path, err)
		}
	}
	return nil
}

// jsonMustMarshal is a small helper used by tests to convert
// state.get output into a comparable JSON string. Returns empty on
// error so test failures are obvious.
func jsonMustMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// _ silences "imported and not used" for ipc when this file alone
// is compiled (some tests may exclude ipc-using code paths).
var _ = ipc.ErrPathNotFound
