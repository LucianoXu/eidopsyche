package relayd

import (
	"context"
	"fmt"
	"net/http"

	"github.com/fiatjaf/khatru"
	gnostr "github.com/nbd-wtf/go-nostr"
)

type Mode string

const (
	ModePaired Mode = "paired"
	ModePublic Mode = "public"
)

type Config struct {
	Mode      Mode
	Listen    string
	OwnerHex  string
	Whitelist *WhitelistSource
}

type Server struct {
	cfg  Config
	r    *khatru.Relay
	http *http.Server
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

	srv := &Server{cfg: cfg, r: r}
	srv.http = &http.Server{Addr: cfg.Listen, Handler: r}
	return srv, nil
}

func (s *Server) ListenAndServe() error              { return s.http.ListenAndServe() }
func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }
func (s *Server) Addr() string                       { return s.http.Addr }
