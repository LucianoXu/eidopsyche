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
}

type messagesData struct {
	Rows       []messageRow
	NextCursor string
	OnlyMal    bool
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

// confirmModalData is the payload for the shared "confirm_modal" template.
// Used in later phases (remove-contact, revoke-invite, gate stop/purge,
// self-update). Defined here so handler tests can exercise the renderer
// before the destructive endpoints land.
type confirmModalData struct {
	Action         string
	Target         string
	Swap           string
	Title          string
	Body           string
	Warning        string
	ExpectedPhrase string
	ConfirmLabel   string
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
