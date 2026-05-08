package dashboard

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// ── phase 5: service control ─────────────────────────────────────────
//
// Routes:
//   GET   /settings/service                       pane: status + buttons
//   POST  /settings/service/reconnect             spawn `gate reconnect`
//   POST  /settings/service/stop                  spawn `gate stop`        (typed-confirm: own label)
//   POST  /settings/service/purge                 spawn `gate purge --yes` (typed-confirm: own label + ack checkbox)
//   POST  /settings/service/self-update           spawn `self-update`      (typed-confirm: target version)
//   GET   /settings/service/confirm/<action>      load typed-confirm modal for stop/purge/self-update
//
// `reconnect` is harmless (the child re-enters the parent over IPC and
// recomputes subscriptions) so it's the only POST that doesn't gate
// behind the typed-confirm modal. The other three actions can take
// the daemon down; the modal asks the operator to type a phrase
// derived from their own state so a stray click can't fire them.

// settingsServiceHandler handles GET /settings/service and routes the
// per-action POSTs through child handlers.
func settingsServiceHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/settings/service" {
			http.NotFound(w, req)
			return
		}
		if req.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx := req.Context()
		if req.Header.Get("HX-Request") == "" {
			renderSettingsFullPage(w, deps, r, logger, ctx, "service")
			return
		}
		renderServicePane(w, r, logger, buildSettingsService(ctx, deps))
	}
}

// settingsServiceActionHandler routes /settings/service/<action> for
// POST (the action) and GET (typed-confirm modal).
func settingsServiceActionHandler(deps DashboardDeps, r *renderer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/settings/service/")
		if path == "" {
			http.NotFound(w, req)
			return
		}
		parts := strings.Split(path, "/")
		switch parts[0] {
		case "reconnect":
			if req.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			handleServiceReconnect(w, req, r, logger, deps)
		case "stop":
			if req.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			handleServiceLifecycle(w, req, r, logger, deps, lifecycleAction{
				name:   "stop",
				args:   []string{"gate", "stop"},
				phrase: func(s ServiceStatus) string { return ownLabelOf(deps) },
			})
		case "purge":
			if req.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			handleServiceLifecycle(w, req, r, logger, deps, lifecycleAction{
				name:              "purge",
				args:              []string{"gate", "purge", "--yes"},
				phrase:            func(s ServiceStatus) string { return ownLabelOf(deps) },
				extraConfirmField: "ack-backup",
				extraConfirmText:  "I have backed up state.db",
			})
		case "self-update":
			if req.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			handleServiceLifecycle(w, req, r, logger, deps, lifecycleAction{
				name:   "self-update",
				args:   []string{"self-update"},
				phrase: func(s ServiceStatus) string { return s.Version },
			})
		case "confirm":
			if req.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if len(parts) < 2 {
				http.NotFound(w, req)
				return
			}
			renderServiceConfirmModal(w, r, logger, deps, parts[1])
		default:
			http.NotFound(w, req)
		}
	}
}

// lifecycleAction is the per-action metadata the lifecycle handler
// needs to render the confirm modal AND validate the typed phrase
// server-side. Centralising this so the modal and the action use the
// same `phrase` function keeps the two ends in sync.
type lifecycleAction struct {
	name              string
	args              []string
	phrase            func(s ServiceStatus) string
	extraConfirmField string // form field name for the ack checkbox (purge only)
	extraConfirmText  string // human-readable label of the same
}

func handleServiceReconnect(w http.ResponseWriter, req *http.Request, r *renderer, logger *slog.Logger, deps DashboardDeps) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	jobID, err := deps.LifecycleRun([]string{"gate", "reconnect"})
	if err != nil {
		if errors.Is(err, ErrLifecycleBusy) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		logger.Warn("dashboard reconnect failed", "err", err)
		http.Error(w, "reconnect failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	renderLifecycleLog(w, r, logger, jobID, "reconnect", false)
}

func handleServiceLifecycle(w http.ResponseWriter, req *http.Request, r *renderer, logger *slog.Logger, deps DashboardDeps, act lifecycleAction) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	expected := act.phrase(deps.Status())
	if err := requireConfirm(req, expected); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if act.extraConfirmField != "" {
		if req.Form.Get(act.extraConfirmField) != "1" {
			http.Error(w, "missing acknowledgement: "+act.extraConfirmText, http.StatusBadRequest)
			return
		}
	}
	jobID, err := deps.LifecycleRun(act.args)
	if err != nil {
		if errors.Is(err, ErrLifecycleBusy) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		logger.Warn("dashboard "+act.name+" failed", "err", err)
		http.Error(w, act.name+" failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	// daemon-killing actions (stop, purge) tear the SSE connection
	// when the parent dies; the lifecycle log template carries the
	// reconnect-when-daemon-restarts banner.
	daemonKilling := act.name == "stop" || act.name == "purge"
	renderLifecycleLog(w, r, logger, jobID, act.name, daemonKilling)
}

func renderServicePane(w http.ResponseWriter, r *renderer, logger *slog.Logger, view settingsServiceData) {
	out, err := r.Render("settings_service", view)
	if err != nil {
		logger.Error("render settings_service", "err", err)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

// renderLifecycleLog emits the streaming-<pre> + status-pill skeleton
// the SSE consumer fills as `lifecycle.line:<jobID>` events arrive.
// The handler returns this fragment immediately on POST; the actual
// child output is delivered out-of-band over /events.
func renderLifecycleLog(w http.ResponseWriter, r *renderer, logger *slog.Logger, jobID, kind string, daemonKilling bool) {
	out, err := r.Render("lifecycle_log", lifecycleLogData{
		JobID:         jobID,
		Kind:          kind,
		DaemonKilling: daemonKilling,
	})
	if err != nil {
		logger.Error("render lifecycle_log", "err", err)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Also OOB-clear the modal slot — the action was invoked from the
	// typed-confirm modal for stop/purge/self-update; for reconnect
	// the slot was empty already, so the OOB swap is a harmless no-op.
	_, _ = w.Write([]byte(out))
	_, _ = w.Write([]byte(`<div id="modal" hx-swap-oob="innerHTML"></div>`))
}

func renderServiceConfirmModal(w http.ResponseWriter, r *renderer, logger *slog.Logger, deps DashboardDeps, action string) {
	var (
		title, body, warning, label, confirmLabel string
		actURL                                    = "/settings/service/" + action
	)
	status := deps.Status()
	switch action {
	case "stop":
		title = "Stop the gate daemon"
		body = "Sends SIGTERM to this daemon. Inbox messages and contacts are preserved on disk; pending sends are dropped."
		warning = "The dashboard will lose its SSE connection when the daemon exits. Restart from a terminal: `eidos gate start`."
		label = ownLabelOf(deps)
		confirmLabel = "Stop daemon"
	case "purge":
		title = "Purge state directory"
		body = "Stops the daemon AND removes the entire state directory: identity key, contacts, inbox, invites, and config. This is irreversible — you'll generate a new pubkey on the next `eidos gate init`."
		warning = "Type your label and tick the backup acknowledgement to confirm. Your peers will need to re-add you from a fresh card after init."
		label = ownLabelOf(deps)
		confirmLabel = "Purge state"
	case "self-update":
		title = "Self-update binary"
		body = fmt.Sprintf("Re-runs the install script to fetch the latest release. The running daemon keeps the OLD binary mapped in memory until restart. Type the running version (%s) to confirm.", status.Version)
		warning = "After the install completes, restart the daemon manually to pick up the new code: `eidos gate stop && eidos gate start`."
		label = status.Version
		confirmLabel = "Run self-update"
	default:
		http.NotFound(w, &http.Request{})
		return
	}
	if label == "" {
		http.Error(w, "confirm phrase is empty (corrupt state?) — refuse to render modal", http.StatusInternalServerError)
		return
	}
	out, rerr := r.Render("confirm_modal", confirmModalData{
		Action:         actURL,
		Target:         "#settings-pane",
		Swap:           "innerHTML",
		Title:          title,
		Body:           body,
		Warning:        warning,
		ExpectedPhrase: label,
		ConfirmLabel:   confirmLabel,
	})
	if rerr != nil {
		logger.Error("render confirm_modal (service)", "err", rerr)
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

// buildSettingsService merges Status() with any in-flight job into a
// single payload for the template. Read-only.
func buildSettingsService(_ context.Context, deps DashboardDeps) settingsServiceData {
	st := deps.Status()
	out := settingsServiceData{
		Version:      st.Version,
		Commit:       st.Commit,
		BuildDate:    st.BuildDate,
		StartedAt:    st.StartedAt,
		StateDir:     st.StateDir,
		DashboardURL: st.DashboardURL,
		IPCSocket:    st.IPCSocket,
		RelayEnabled: st.RelayEnabled,
		RelayMode:    st.RelayMode,
		RelayListen:  st.RelayListen,
		OwnLabel:     ownLabelOf(deps),
	}
	if st.ActiveJobID != "" {
		out.ActiveJobID = st.ActiveJobID
		out.ActiveJobArgs = st.ActiveJobArgs
		out.ActiveJobAt = st.ActiveJobAt
	}
	return out
}

// ownLabelOf reads the dashboard's own label, defaulting to empty
// string on error. Used as the typed-confirm phrase for stop/purge —
// if the lookup fails the modal renders with an empty phrase, the
// caller bails on the empty-phrase guard, and the operator gets a
// clear 500 instead of a fall-through.
func ownLabelOf(deps DashboardDeps) string {
	label, err := deps.OwnLabel(context.Background())
	if err != nil {
		return ""
	}
	return label
}
