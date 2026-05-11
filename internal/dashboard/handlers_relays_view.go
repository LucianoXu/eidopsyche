package dashboard

import (
	"log/slog"
	"net/http"
	"time"
)

// relaysData is the template payload for the relay-health panel.
type relaysData struct {
	Rows []relayRow
}

type relayRow struct {
	URL          string
	Role         string
	State        string
	LastError    string
	LastEventAgo string
}

func buildRelaysView(deps DashboardDeps) relaysData {
	now := time.Now().Unix()
	snap := deps.ListRelayHealth()
	rows := make([]relayRow, 0, len(snap))
	for _, h := range snap {
		ago := "—"
		if h.LastEventAt > 0 {
			d := time.Duration(now-h.LastEventAt) * time.Second
			ago = humanSince(d)
		}
		rows = append(rows, relayRow{
			URL:          h.URL,
			Role:         h.Role,
			State:        h.State,
			LastError:    h.LastError,
			LastEventAgo: ago,
		})
	}
	// Stable order: home first, fallback, contact, extra; URL alphabetic within.
	sortRelayRows(rows)
	return relaysData{Rows: rows}
}

func relaysHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		out, err := r.Render("relays", buildRelaysView(deps))
		if err != nil {
			logger.Error("render relays", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

// sortRelayRows orders rows by role priority (home > fallback > contact >
// extra > anything else) then alphabetically by URL within each role.
func sortRelayRows(rows []relayRow) {
	rank := func(role string) int {
		switch role {
		case "home":
			return 0
		case "fallback":
			return 1
		case "contact":
			return 2
		case "extra":
			return 3
		default:
			return 4
		}
	}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			a, b := rows[j-1], rows[j]
			if rank(a.Role) < rank(b.Role) {
				break
			}
			if rank(a.Role) == rank(b.Role) && a.URL <= b.URL {
				break
			}
			rows[j-1], rows[j] = b, a
		}
	}
}
