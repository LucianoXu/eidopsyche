package dashboard

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

// ---------------------------------------------------------------- *
// Phase 1 — Settings (Operator panel)
//
// Routes:
//   GET  /settings                       (renders shell + Identity tab)
//   GET  /settings/identity              (Identity pane fragment)
//   POST /settings/identity/label        (set own label)
//   GET  /settings/config                (Config pane fragment)
//   POST /settings/config                (set one config key)
// ---------------------------------------------------------------- */

const labelMaxLen = 64

// settingsShellHandler renders /settings. With HX-Request set (e.g. the
// sidebar Operator → Settings link clicked from an already-loaded page),
// it returns just the "settings" fragment so htmx can swap it into
// #main without nesting a whole document. On a cold address-bar load,
// it returns the full layout (topbar + sidebar + settings pane) so the
// page stands on its own.
func settingsShellHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/settings" {
			http.NotFound(w, req)
			return
		}
		if req.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx := req.Context()
		shell := buildSettingsShell(ctx, deps, "identity")

		if req.Header.Get("HX-Request") != "" {
			out, err := r.Render("settings", shell)
			if err != nil {
				logger.Error("render settings", "err", err)
				http.Error(w, "render failed", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(out))
			return
		}

		// Cold load: render the full document.
		renderSettingsFullPage(w, deps, r, logger, ctx, "identity")
	}
}

func settingsIdentityHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx := req.Context()
		// Direct navigation (e.g. address bar) lacks the HX-Request
		// header; in that case render the full shell so the page is
		// not just a fragment hanging in space.
		if req.Header.Get("HX-Request") == "" {
			renderSettingsFullPage(w, deps, r, logger, ctx, "identity")
			return
		}
		out, err := r.Render("settings_identity", buildSettingsIdentity(ctx, deps))
		if err != nil {
			logger.Error("render settings_identity", "err", err)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}

func settingsLabelPostHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := req.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		raw := req.Form.Get("label")
		label := strings.TrimSpace(raw)
		ctx := req.Context()

		// Start from the current persisted snapshot — Label and CardURI
		// reflect what's actually saved; FormLabel will be set below
		// to either the just-saved value or the rejected raw input so
		// the input shows the right thing without ever poisoning the
		// colophon with unsaved data.
		out := buildSettingsIdentity(ctx, deps)
		switch {
		case label == "":
			out.Error = "Label cannot be empty."
		case len([]rune(label)) > labelMaxLen:
			out.Error = fmt.Sprintf("Label too long (%d runes; max %d).", len([]rune(label)), labelMaxLen)
		default:
			if err := deps.SetOwnLabel(ctx, label); err != nil {
				logger.Warn("dashboard set-label failed", "err", err)
				out.Error = "Save failed: " + err.Error()
			} else {
				// Persisted: pull a fresh snapshot so Label, CardURI,
				// and FormLabel all reflect the new state atomically.
				out = buildSettingsIdentity(ctx, deps)
				out.Saved = true
			}
		}
		if out.Error != "" {
			// Preserve the operator's raw input in the form input only.
			// out.Label still holds the persisted label so the colophon
			// keeps showing the truth.
			out.FormLabel = raw
		}
		rendered, rerr := r.Render("settings_identity", out)
		if rerr != nil {
			logger.Error("render settings_identity", "err", rerr)
			http.Error(w, "render failed", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Even on validation error we return 200; htmx default doesn't
		// swap 4xx, and the form-flash chip carries the message.
		_, _ = w.Write([]byte(rendered))
	}
}

func settingsConfigHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		switch req.Method {
		case http.MethodGet:
			if req.Header.Get("HX-Request") == "" {
				renderSettingsFullPage(w, deps, r, logger, ctx, "config")
				return
			}
			out, err := r.Render("settings_config", buildSettingsConfig(deps, "", "", ""))
			if err != nil {
				logger.Error("render settings_config", "err", err)
				http.Error(w, "render failed", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(out))

		case http.MethodPost:
			if err := req.ParseForm(); err != nil {
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			path := strings.TrimSpace(req.Form.Get("path"))
			value := req.Form.Get("value")
			if _, ok := config.KeyByPath(path); !ok {
				http.Error(w, "unknown config key", http.StatusBadRequest)
				return
			}

			var rowErr string
			if err := deps.ConfigSet(ctx, path, value); err != nil {
				rowErr = err.Error()
				logger.Warn("dashboard config-set failed", "err", err, "path", path)
			}

			// Render only the affected row so htmx can swap-out-of-place
			// — every other row stays intact.
			snap, err := deps.ConfigSnapshot()
			if err != nil {
				logger.Error("config snapshot after set", "err", err)
				http.Error(w, "render failed", 500)
				return
			}
			row := configRowFromKey(path, snap, rowErr)
			rendered, rerr := r.Render("settings_config_row", row)
			if rerr != nil {
				logger.Error("render settings_config_row", "err", rerr)
				http.Error(w, "render failed", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(rendered))

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// renderSettingsFullPage emits the whole layout (topbar + sidebar +
// settings pane) so deep-link or address-bar navigation works without a
// preceding "/" load.
func renderSettingsFullPage(
	w http.ResponseWriter, deps DashboardDeps, r *renderer, logger *slog.Logger,
	ctx context.Context, active string,
) {
	label, _ := deps.OwnLabel(ctx)
	side := buildSidebar(ctx, deps, "", true)
	shell := buildSettingsShell(ctx, deps, active)
	mainHTML, err := r.Render("settings", shell)
	if err != nil {
		logger.Error("render settings", "err", err)
		http.Error(w, "render failed", 500)
		return
	}
	out, err := r.Render("shell", shellData{
		OwnLabel: label,
		OwnNpub:  deps.OwnPubkey(),
		Sidebar:  side,
		Main:     template.HTML(mainHTML), //nolint:gosec // trusted internal template output
	})
	if err != nil {
		logger.Error("render shell", "err", err)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

// ── settings view-builders ──────────────────────────────────────────

func buildSettingsShell(ctx context.Context, deps DashboardDeps, active string) settingsShellData {
	out := settingsShellData{Active: active}
	out.OwnLabel, _ = deps.OwnLabel(ctx)
	switch active {
	case "identity":
		v := buildSettingsIdentity(ctx, deps)
		out.Identity = &v
	case "contacts":
		v := buildSettingsContacts(ctx, deps, "")
		out.Contacts = &v
	case "config":
		v := buildSettingsConfig(deps, "", "", "")
		out.Config = &v
	case "invites":
		v := buildSettingsInvites(ctx, deps)
		out.Invites = &v
	case "relays":
		v := buildSettingsRelays(ctx, deps, "")
		out.Relays = &v
	case "service":
		v := buildSettingsService(ctx, deps, nil) // shell-render path: no logger handy
		out.Service = &v
	}
	return out
}

func buildSettingsIdentity(ctx context.Context, deps DashboardDeps) settingsIdentityData {
	label, _ := deps.OwnLabel(ctx)
	npub := pubkeyToNpub(deps.OwnPubkey())
	cardURI, _ := deps.OwnCardURI(ctx)
	return settingsIdentityData{
		Label:     label,
		FormLabel: label, // input prefills with the current value
		Npub:      npub,
		Hex:       deps.OwnPubkey(),
		CardURI:   cardURI,
	}
}

func buildSettingsConfig(deps DashboardDeps, errMsg, errPath, errMsgRow string) settingsConfigData {
	out := settingsConfigData{Error: errMsg}
	snap, err := deps.ConfigSnapshot()
	if err != nil {
		out.Error = "Could not load config.toml: " + err.Error()
		return out
	}
	for _, k := range config.KeyList() {
		row := configRowFromKey(k.Path, snap, "")
		if k.Path == errPath {
			row.Error = errMsgRow
		}
		out.Rows = append(out.Rows, row)
	}
	return out
}

func configRowFromKey(path string, snap config.Config, rowErr string) settingsConfigRow {
	k, ok := config.KeyByPath(path)
	if !ok {
		return settingsConfigRow{Path: path, Slug: slugifyPath(path), Editable: false, Error: "unknown key"}
	}
	return settingsConfigRow{
		Path:        k.Path,
		Slug:        slugifyPath(k.Path),
		Description: k.Description,
		Value:       k.Get(&snap),
		Editable:    true,
		Error:       rowErr,
	}
}

func slugifyPath(path string) string { return strings.ReplaceAll(path, ".", "-") }

// isSettingsURL parses raw as a URL and reports whether the path is
// /settings or any /settings/ sub-path. Used to flag the sidebar
// Settings entry as active across SSE-triggered refreshes. Resilient
// to absolute or path-only inputs and rejects unrelated paths whose
// query string just happens to contain "/settings".
func isSettingsURL(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	p := u.Path
	return p == "/settings" || strings.HasPrefix(p, "/settings/")
}
