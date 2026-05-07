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
			if len(s) <= n {
				return s
			}
			return s[:n] + "…"
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
