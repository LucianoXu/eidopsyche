package dashboard

import (
	"log/slog"
	"net/http"
)

// sseHub wires DashboardDeps.SubscribeEvents to an HTTP SSE handler.
// Implementation grows in a later task; this scaffold keeps the
// /events route registrable.
type sseHub struct {
	deps DashboardDeps
}

func newSSEHub(deps DashboardDeps) *sseHub { return &sseHub{deps: deps} }

func (h *sseHub) handler(_ *renderer, _ *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "sse not yet implemented", http.StatusNotImplemented)
	}
}
