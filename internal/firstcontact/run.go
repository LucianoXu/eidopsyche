package firstcontact

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/store"
)

// Deps is the full input to Run. Production wiring lives in
// cmd/eidos/summon; tests substitute fakes for any field.
type Deps struct {
	StateDir       string
	Renderer       render.Renderer
	Claude         *Claude
	ReadyDeps      ReadyDeps
	DockerClient   forgectl.Client
	Image          string
	WriteVolume    VolumeWriter
	StartContainer func(ctx context.Context, slug string) error
	ResponseWait   ResponseWaiter
	AddContact     ContactAdder
	ExistingSlugs  func() ([]string, error)
}

// Run drives the wizard end-to-end. Returns the rendered Summoning
// (the in-memory state — never persisted) plus the response body the
// agent wrote at journal/0000-response.md. The cmd-level caller passes
// the body through r.Typewriter and prints a one-line completion.
func Run(ctx context.Context, d Deps) (*Summoning, []byte, error) {
	s := &Summoning{}
	subsequent, err := isSubsequentRun(d.StateDir)
	if err != nil {
		return nil, nil, err
	}
	s.Subsequent = subsequent

	if !subsequent {
		if err := Phase0(ctx, s, d.Renderer); err != nil {
			return nil, nil, err
		}
		ready := StartBackground(ctx, d.ReadyDeps)
		if err := Phase1(ctx, s, d.Renderer, Phase1Deps{StateDir: d.StateDir}); err != nil {
			if errors.Is(err, ErrSelfHostExit) {
				return s, nil, ErrSelfHostExit
			}
			return s, nil, err
		}
		existing, _ := d.ExistingSlugs()
		if err := Phase2(ctx, s, d.Renderer, d.Claude, Phase2Deps{ExistingSlugs: existing}); err != nil {
			return s, nil, err
		}
		body, err := Phase3(ctx, s, d.Renderer, d.Claude, ready, Phase3Deps{
			DockerClient: d.DockerClient, Image: d.Image,
			WriteVolume: d.WriteVolume, ContainerStart: d.StartContainer,
			ResponseWait: d.ResponseWait, AddContact: d.AddContact,
		})
		return s, body, err
	}

	// Subsequent run: load operator state, skip phase 0/1.
	if err := loadOperatorIntoSummoning(d.StateDir, s); err != nil {
		return s, nil, err
	}
	d.Renderer.Show(fmt.Sprintf(stringFor(s.Lang, "welcome_back"), s.OperatorLabel))
	ready := StartBackground(ctx, d.ReadyDeps)
	existing, _ := d.ExistingSlugs()
	if err := Phase2(ctx, s, d.Renderer, d.Claude, Phase2Deps{ExistingSlugs: existing}); err != nil {
		return s, nil, err
	}
	body, err := Phase3(ctx, s, d.Renderer, d.Claude, ready, Phase3Deps{
		DockerClient: d.DockerClient, Image: d.Image,
		WriteVolume: d.WriteVolume, ContainerStart: d.StartContainer,
		ResponseWait: d.ResponseWait, AddContact: d.AddContact,
	})
	return s, body, err
}

// IsSubsequentRun reports whether the wizard should skip phases 0 and
// 1 (operator self) because the state directory already holds an
// initialized identity. Exported so cmd/eidos/main can decide whether
// bare `eidos` should auto-dispatch to the wizard or fall through to
// cobra's default help.
func IsSubsequentRun(stateDir string) bool {
	v, _ := isSubsequentRun(stateDir)
	return v
}

func isSubsequentRun(stateDir string) (bool, error) {
	if _, err := os.Stat(filepath.Join(stateDir, "key")); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if _, err := os.Stat(filepath.Join(stateDir, "state.db")); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

func loadOperatorIntoSummoning(stateDir string, s *Summoning) error {
	k, err := identity.LoadKey(filepath.Join(stateDir, "key"))
	if err != nil {
		return fmt.Errorf("load operator key: %w", err)
	}
	s.OperatorNpub = k.Npub

	// Restore the rest of the operator profile from state.db. Without
	// this, subsequent runs reach phase 3 with empty Label / HomeRelay
	// and forge.Orchestrate hands init-volume blank EIDOS_FORGE_LABEL /
	// EIDOS_FORGE_RELAY env vars, which the in-container init refuses
	// (cmd/eidos/forge/init_volume.go:71).
	dbPath := filepath.Join(stateDir, "state.db")
	db, err := store.Open(dbPath, true) // read-only is enough
	if err != nil {
		return fmt.Errorf("open state.db: %w", err)
	}
	defer db.Close()
	ctx := context.Background()
	label, err := db.GetMeta(ctx, "label")
	if err != nil {
		return fmt.Errorf("read label meta: %w", err)
	}
	s.OperatorLabel = label
	var home string
	err = db.QueryRowContext(ctx,
		`SELECT relay_url FROM own_relays WHERE role='home' LIMIT 1`).Scan(&home)
	if err != nil {
		return fmt.Errorf("read home relay: %w", err)
	}
	s.HomeRelay = home
	s.Lang = "zh"
	return nil
}
