package dashboard

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

// Run starts the dashboard HTTP server and blocks until ctx is cancelled.
// Returns nil on clean shutdown and on configuration-driven no-ops
// (disabled, non-loopback bind refusal, listen failure).
func Run(ctx context.Context, deps DashboardDeps, cfg config.DashboardConfig, logger *slog.Logger) error {
	if !cfg.Enabled {
		logger.Info("dashboard disabled by config")
		return nil
	}
	if !isLoopback(cfg.Listen) {
		logger.Error("dashboard non-loopback bind not supported in v1; skipping", "listen", cfg.Listen)
		return nil
	}

	mux := http.NewServeMux()
	registerHandlers(mux, deps, logger)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           sameOriginGuard(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	listenErr := make(chan error, 1)
	go func() {
		logger.Info("dashboard listening", "url", "http://"+cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
		close(listenErr)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-listenErr:
		if err != nil {
			logger.Error("dashboard listen failed", "err", err)
		}
		return nil
	}
}

// isLoopback reports whether the host portion of "host:port" is a loopback
// address. A bare port (e.g. ":22893") is treated as non-loopback because
// it binds all interfaces.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// sameOriginGuard rejects requests whose Origin header does not match Host.
// Browsers omit Origin on safe-method address-bar navigation (GET/HEAD), so
// a missing Origin is treated as same-origin only for those methods. For
// any other method (POST, PUT, DELETE, ...) Origin must be present AND
// match Host; an empty Origin or the literal string "null" (sandboxed
// iframes, file://, some non-standard contexts) is rejected for mutating
// methods so cross-origin contexts cannot bypass the check.
func sameOriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		safeMethod := r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions

		if origin == "" {
			if safeMethod {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "missing Origin on mutating request", http.StatusForbidden)
			return
		}
		if origin == "null" {
			http.Error(w, "Origin: null rejected", http.StatusForbidden)
			return
		}
		if i := strings.Index(origin, "://"); i >= 0 {
			origin = origin[i+3:]
		}
		if origin != r.Host {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// registerHandlers wires up all dashboard routes. Templates are parsed once
// here at startup; on parse failure the dashboard returns 500 on every
// request so the operator notices.
func registerHandlers(mux *http.ServeMux, deps DashboardDeps, logger *slog.Logger) {
	r, err := newRenderer()
	if err != nil {
		logger.Error("dashboard renderer init failed", "err", err)
		mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
			http.Error(w, "dashboard renderer unavailable", 500)
		})
		return
	}
	registerHandlersWithRenderer(mux, deps, r, logger)
}
