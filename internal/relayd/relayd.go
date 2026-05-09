package relayd

import (
	"context"
	"fmt"
	"net/http"

	"github.com/fiatjaf/eventstore/sqlite3"
	"github.com/fiatjaf/khatru"
	gnostr "github.com/nbd-wtf/go-nostr"
)

type Mode string

const (
	ModePaired Mode = "paired"
	ModePublic Mode = "public"
)

type Config struct {
	Mode           Mode
	Listen         string
	OwnerHex       string
	Whitelist      *WhitelistSource
	TLS            TLSConfig
	Auth           AuthConfig
	EventStorePath string // Empty = ephemeral (default); non-empty enables sqlite persistence.
}

// AuthConfig governs NIP-42 AUTH enforcement. Required defaults to false
// at the struct level (the spec-correct default of true is set by
// config.Defaults() at the call site, so unit tests of relayd that don't
// go through the config layer pick whichever posture they explicitly want).
type AuthConfig struct {
	Required   bool
	ServiceURL string // optional; overrides khatru's auto-derived URL
}

// TLSConfig governs whether the relay terminates TLS itself. Both fields
// must be set (or both empty) — setting only one is a startup error in
// ListenAndServe. No autocert in this release.
type TLSConfig struct {
	CertFile string
	KeyFile  string
}

type Server struct {
	cfg        Config
	r          *khatru.Relay
	http       *http.Server
	eventStore *sqlite3.SQLite3Backend
}

func New(cfg Config) (*Server, error) {
	r := khatru.NewRelay()
	// Info is a *nip11.RelayInformationDocument, initialized by NewRelay.
	r.Info.Name = "eidos-gate-relay"
	r.Info.Software = "eidopsyche"

	switch cfg.Mode {
	case ModePaired:
		if cfg.OwnerHex == "" {
			return nil, fmt.Errorf("paired mode requires OwnerHex")
		}
		if cfg.Whitelist == nil {
			return nil, fmt.Errorf("paired mode requires Whitelist")
		}
		r.RejectEvent = append(r.RejectEvent,
			func(ctx context.Context, event *gnostr.Event) (bool, string) {
				if event.Kind != 1059 {
					return true, "blocked: only kind 1059 accepted"
				}
				for _, tag := range event.Tags {
					if len(tag) >= 2 && tag[0] == "p" && tag[1] == cfg.OwnerHex {
						return false, ""
					}
				}
				return true, "blocked: not addressed to owner"
			},
		)
	case ModePublic:
		// Accept anything well-formed. khatru validates signatures by default.
	default:
		return nil, fmt.Errorf("unknown mode %q", cfg.Mode)
	}

	if cfg.Auth.ServiceURL != "" {
		r.ServiceURL = cfg.Auth.ServiceURL
	}

	if cfg.Auth.Required {
		r.RejectFilter = append(r.RejectFilter, requireAuthForKind1059Reads)
	}

	srv := &Server{cfg: cfg, r: r}

	if cfg.EventStorePath != "" {
		evStore, err := OpenSQLiteStore(cfg.EventStorePath)
		if err != nil {
			return nil, err
		}
		r.StoreEvent = append(r.StoreEvent, evStore.SaveEvent)
		r.QueryEvents = append(r.QueryEvents, evStore.QueryEvents)
		r.CountEvents = append(r.CountEvents, evStore.CountEvents)
		r.DeleteEvent = append(r.DeleteEvent, evStore.DeleteEvent)
		r.ReplaceEvent = append(r.ReplaceEvent, evStore.ReplaceEvent)
		srv.eventStore = evStore
	}

	srv.http = &http.Server{Addr: cfg.Listen, Handler: r}
	return srv, nil
}

// requireAuthForKind1059Reads enforces NIP-17 §Recommendations: a REQ
// for kind:1059 events is only served to a NIP-42-authenticated client
// whose authed pubkey matches every #p tag value in the filter. The
// first unauthenticated REQ also pushes a fresh AUTH challenge so the
// client knows what to do.
//
// Per NIP-01, an absent or empty `kinds` array matches all kinds —
// including 1059. We must therefore treat such filters as "potentially
// covers 1059" and require AUTH, not as "doesn't mention 1059, pass".
// Otherwise an unauthenticated client could read every gift wrap by
// simply omitting kinds.
func requireAuthForKind1059Reads(ctx context.Context, filter gnostr.Filter) (bool, string) {
	mayMatchKind1059 := len(filter.Kinds) == 0
	if !mayMatchKind1059 {
		for _, k := range filter.Kinds {
			if k == 1059 {
				mayMatchKind1059 = true
				break
			}
		}
	}
	if !mayMatchKind1059 {
		return false, ""
	}
	authed := khatru.GetAuthed(ctx)
	if authed == "" {
		khatru.RequestAuth(ctx)
		return true, "auth-required: NIP-42 AUTH required for kind:1059 reads"
	}
	pTags, ok := filter.Tags["p"]
	if !ok || len(pTags) == 0 {
		return true, "auth-required: kind:1059 REQ must include #p filter"
	}
	for _, p := range pTags {
		if p != authed {
			return true, "auth-mismatch: requested #p does not match AUTH pubkey"
		}
	}
	return false, ""
}

// ListenAndServe blocks until the listener exits. When both TLS.CertFile
// and TLS.KeyFile are set, it serves wss:// via http.ListenAndServeTLS.
// Setting only one is a startup error (the asymmetry would otherwise be
// silent and the user would see a TLS handshake failure with no
// indication that the configuration was the cause).
func (s *Server) ListenAndServe() error {
	if s.cfg.TLS.CertFile != "" || s.cfg.TLS.KeyFile != "" {
		if s.cfg.TLS.CertFile == "" || s.cfg.TLS.KeyFile == "" {
			return fmt.Errorf("relay.tls: both cert_file and key_file must be set (got cert=%q key=%q)",
				s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile)
		}
		return s.http.ListenAndServeTLS(s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile)
	}
	return s.http.ListenAndServe()
}
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.http.Shutdown(ctx)
	if s.eventStore != nil {
		s.eventStore.Close()
	}
	return err
}

// Close forces an immediate stop: the listener closes and all active
// connections (including upgraded WebSockets) are terminated. Use when
// graceful drain isn't appropriate — chiefly tests that simulate a relay
// disappearing under a daemon's feet.
func (s *Server) Close() error {
	err := s.http.Close()
	if s.eventStore != nil {
		s.eventStore.Close()
	}
	return err
}
func (s *Server) Addr() string { return s.http.Addr }
