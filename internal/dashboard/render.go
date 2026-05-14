package dashboard

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"sort"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

type renderer struct {
	t *template.Template
}

func newRenderer() (*renderer, error) {
	funcs := template.FuncMap{
		"reltime": func(t time.Time) string { return relativeTime(t, time.Now()) },
		"truncate": func(n int, s string) string {
			// Rune-aware: avoid splitting multi-byte UTF-8 characters
			// (CJK, emoji) in the middle of a glyph.
			r := []rune(s)
			if len(r) <= n {
				return s
			}
			return string(r[:n]) + "…"
		},
	}
	t, err := template.New("").Funcs(funcs).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &renderer{t: t}, nil
}

func (r *renderer) Render(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := r.t.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func staticSubFS() (fs.FS, error) { return fs.Sub(staticFS, "static") }

// relativeTime renders a duration like "14m ago", "2h ago", "1d ago".
func relativeTime(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// ---- view-data structs (the only contract with the templates) ----

type shellData struct {
	OwnLabel string
	OwnNpub  string
	Sidebar  sidebarData
	Main     template.HTML
}

type sidebarData struct {
	Contacts []sidebarContact
	// ActiveSettings flips on the Settings sidebar entry's `active`
	// styling when the operator is on any /settings* route. Each main
	// route handler that renders the shell sets it explicitly.
	ActiveSettings bool
}

type sidebarContact struct {
	Pubkey   string
	Label    string
	Tier     contacts.Tier
	LastSeen *time.Time
	Active   bool
}

type threadData struct {
	Counterpart sidebarContact
	Bubbles     []bubbleData
	OwnPubkey   string
}

type bubbleData struct {
	Self         bool
	From         string
	Text         string
	At           time.Time
	Malformed    bool
	RejectReason string
	EventID      string
	// Status is one of "" / "sent" / "delivered". Only meaningful when Self
	// is true; renders as ✓ for sent (relay accepted) and ✓✓ for delivered
	// (peer-confirmed receipt via tier-2 ack).
	Status string
	// OOB, when true, renders the outer bubble div with
	// `hx-swap-oob='outerHTML:[data-event-id="<EventID>"]'` so htmx
	// REPLACES an existing bubble of the same EventID instead of
	// appending a new one. Used by the SSE outbox.message channel to
	// flip ✓ → ✓✓ on the bubble that the form-submit response already
	// placed in the thread.
	OOB bool
}

type messagesData struct {
	Rows       []messageRow
	NextCursor string
	OnlyMal    bool
	// Pending is true when the panel is currently showing the unknown-
	// sender view (?view=pending). The template uses it to highlight the
	// active chip and label the panel "Pending" instead of "Inbox".
	Pending bool
	// PendingCount is the number of currently-pending inbox rows (capped
	// at messagesPageSize+1, used by the template to render a count chip
	// or a "50+" badge when the cap is hit). Always populated regardless
	// of which view is active.
	PendingCount int
}

type messageRow struct {
	Direction    string
	From         string
	Preview      string
	At           time.Time
	Malformed    bool
	RejectReason string
	EventID      string
	Pubkey       string
	// Pending tags an inbox row whose sender is not a known contact.
	// The template renders it with a subdued style + "Promote to contact"
	// affordance so the operator can decide whether to keep talking.
	// Always false for outbox rows.
	Pending bool
}

type composeData struct {
	Pubkey string
	Error  string
}

// ── phase 1: settings ───────────────────────────────────────────────
//
// settingsShellData is the payload for the "settings" template. Active
// names which sub-tab is current ("identity" or "config"); the matching
// pointer field is populated and the others are nil.

type settingsShellData struct {
	OwnLabel string
	Active   string
	Identity *settingsIdentityData
	Contacts *settingsContactsData
	Config   *settingsConfigData
	Invites  *settingsInvitesData
	Relays   *settingsRelaysData
	Service  *settingsServiceData
}

// ── phase 2: contacts ──────────────────────────────────────────────

type settingsContactsData struct {
	Rows  []contactRow
	Error string
}

type contactRow struct {
	Pubkey      string
	PubkeyShort string // first 12 hex chars for stable DOM ids
	Npub        string
	Label       string
	Tier        contacts.Tier
	LastSeen    *time.Time
}

type contactDetailData struct {
	Pubkey      string
	PubkeyShort string
	Npub        string
	Label       string
	FormLabel   string // separate from Label so a rejected entry doesn't poison the colophon
	Tier        contacts.Tier
	Relays      []string
	LabelError  string
	LabelSaved  bool
	TierError   string
	TierSaved   bool
}

type contactScanData struct {
	CardURI        string
	LabelOverride  string // operator's typed label override; preserved through preview → confirm
	Pubkey         string
	Npub           string
	Label          string
	Relay          string
	AlreadyContact bool
	Error          string
}

type settingsIdentityData struct {
	// Label is the currently-persisted label (read from the meta store).
	// Renders in the colophon as the authoritative "Label" value.
	Label string
	// FormLabel is the value to prefill the rename input with. On
	// success this matches Label; on validation failure it preserves
	// the operator's rejected input so they can edit and retry without
	// retyping. Kept separate from Label so a rejected entry never
	// poses as the current identity.
	FormLabel string
	Npub      string
	Hex       string
	CardURI   string
	Saved     bool
	Error     string
}

type settingsConfigData struct {
	Rows  []settingsConfigRow
	Error string
}

type settingsConfigRow struct {
	Path        string
	Slug        string
	Description string
	Value       string
	Editable    bool
	Error       string
}

// ── phase 3: invites ───────────────────────────────────────────────

// settingsInvitesData is the payload for the "settings_invites"
// template. Active / Expired / Revoked are pre-bucketed so the
// template renders three sections without repeating the filter
// logic in templates. CreateError is rendered above the create form
// when CreateInvite fails; RedeemError above the redeem form;
// RedeemResult is populated on a successful redeem so the operator
// sees confirmation (issuer npub + accepted-by relays) without reloading.
type settingsInvitesData struct {
	Active        []inviteRow
	Expired       []inviteRow
	Revoked       []inviteRow
	CreateError   string
	CreatedInvite *createdInvite
	RedeemError   string
	RedeemResult  *redeemFlash
	Error         string
}

// inviteRow is the per-row payload for the "invite_row" template.
// All rendered fields are either plain strings (so html/template
// auto-escaping applies) or numbers; no template.HTML escapes pass
// through.
type inviteRow struct {
	ID            string // full id (used for stable dom ids and revoke action)
	IDShort       string // first 8 hex chars — what the operator types to revoke
	CreatedAt     time.Time
	ExpiresAt     time.Time // zero ⇒ never
	MaxUses       int       // 0 ⇒ unlimited
	Uses          int
	IssuerLabel   string
	RedeemerLabel string
	Status        string // "active" / "expired" / "revoked"
}

// createdInvite is the success-flash shown immediately after the
// operator hits Create — the URI must be visible exactly once,
// foregrounded, with a copy-button. Subsequent reloads re-render
// the row in the Active section without the URI (it's not stored).
type createdInvite struct {
	IDShort   string
	URI       string
	ExpiresAt time.Time // zero ⇒ never
	MaxUses   int
	SingleUse bool
	Unlimited bool
	Redeemer  string
}

// redeemFlash is the success-flash shown after a redeem completes.
type redeemFlash struct {
	IssuerNpub  string
	IssuerRelay string
	AcceptedBy  []string
}

// confirmModalData is the payload for the shared "confirm_modal" template.
// Used in remove-contact, revoke-invite, gate stop/purge/self-update.
//
// AckField, when non-empty, renders an additional checkbox in the modal
// — its form name is AckField and the danger button stays disabled
// until BOTH the typed phrase matches AND the checkbox is checked.
// Server-side, the corresponding handler must verify req.Form.Get(
// AckField) == "1". Used by Phase 5's purge action ("I have backed up
// state.db" acknowledgement) so the operator can't fire the
// state-wiping action with just the typed-confirm.
type confirmModalData struct {
	Action         string
	Target         string
	Swap           string
	Title          string
	Body           string
	Warning        string
	ExpectedPhrase string
	ConfirmLabel   string
	AckField       string // optional checkbox form-field name (purge: "ack-backup")
	AckText        string // human-readable label for the checkbox
}

// ── phase 4: own relays ────────────────────────────────────────────

// settingsRelaysData is the payload for the "settings_relays" template.
// Rows merge own_relays state with the daemon's per-URL connection
// state — the operator sees role + URL + status + last-error in one
// table. AddError appears above the add form on validation failure;
// HomeCount is exposed so the template can grey-out the Remove button
// on the only home relay (defense-in-depth alongside the daemon's own
// errOwnRelayHomeRequired refusal).
type settingsRelaysData struct {
	Rows      []ownRelayRow
	HomeCount int
	AddError  string
	Error     string
}

// ── phase 5: service control ───────────────────────────────────────

// settingsServiceData is the payload for settings_service.html.
type settingsServiceData struct {
	Version      string
	Commit       string
	BuildDate    string
	StartedAt    time.Time
	StateDir     string
	DashboardURL string
	IPCSocket    string
	OwnLabel     string

	// ActiveJob* fields are non-empty when a lifecycle job is in
	// flight. Used to disable the action buttons (single-job-at-a-time
	// invariant) and surface what's running.
	ActiveJobID   string
	ActiveJobArgs []string
	ActiveJobAt   time.Time
}

// lifecycleLogData is the payload for lifecycle_log.html — the
// streaming-<pre> + status-pill skeleton the operator sees as soon as
// they confirm an action.
type lifecycleLogData struct {
	JobID         string
	Kind          string // "reconnect" / "stop" / "purge" / "self-update"
	DaemonKilling bool
}

// ownRelayRow is the per-row payload for the "relay_row" template.
// Slug is a stable hex-of-URL fragment used in DOM ids; URLs contain
// characters (// : .) that aren't valid in CSS selectors or hx-target
// queries.
type ownRelayRow struct {
	URL            string
	Slug           string
	Role           string
	AddedAt        time.Time
	State          string // "connected", "connecting", "error", "auth-failed", "pending", "unknown"
	LastError      string
	LastEventAgo   string // "5s" / "12m" / "1d"
	LastEventKnown bool   // true iff health snapshot has a non-zero LastEventAt
	IsLastHome     bool   // true when role==home AND HomeCount==1

	// Publish health is orthogonal to subscription State: a relay can
	// be "connected" (sub is alive) while rejecting every publish, or
	// vice versa. PublishKnown == false means no publish has been
	// attempted yet; the template renders nothing in that case.
	PublishKnown bool
	PublishOk    bool
	PublishErr   string // empty when PublishOk
	PublishAgo   string // "5s" / "12m" / "1d"
}

// sortContactsForSidebar orders contacts by most-recent activity, then by
// tier rank (master > friend > acquaintance > blocked), then by label.
func sortContactsForSidebar(in []sidebarContact) []sidebarContact {
	out := make([]sidebarContact, len(in))
	copy(out, in)
	tierRank := map[contacts.Tier]int{
		contacts.TierMaster:       0,
		contacts.TierFriend:       1,
		contacts.TierAcquaintance: 2,
		contacts.TierBlocked:      3,
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := out[i].LastSeen, out[j].LastSeen
		switch {
		case ai != nil && aj == nil:
			return true
		case ai == nil && aj != nil:
			return false
		case ai != nil && aj != nil && !ai.Equal(*aj):
			return ai.After(*aj)
		}
		if r := tierRank[out[i].Tier] - tierRank[out[j].Tier]; r != 0 {
			return r < 0
		}
		return out[i].Label < out[j].Label
	})
	return out
}
