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

// EntryMode tells the wizard which CLI surface invoked it. Phase 2's
// option set varies by entry mode (see spec § 4.2).
type EntryMode int

const (
	// EntryBareEidos is bare `eidos` — auto-dispatched when the host
	// has no local identity. Phase 2 offers the full menu including 退出.
	EntryBareEidos EntryMode = iota
	// EntrySummon is `eidos summon`. Phase 2 suppresses 退出 (the user
	// already committed to summoning by typing `summon`).
	EntrySummon
	// EntryGateInit is `eidos gate init`. Phase 2 is short-circuited to
	// Exit (the user wants only an identity, no mind-form).
	EntryGateInit
)

// Deps is the full input to Run. Production wiring lives in
// cmd/eidos/summon and cmd/eidos/gate; tests substitute fakes for any
// field.
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

	// EntryMode names which CLI surface invoked the wizard.
	EntryMode EntryMode
	// MasterCardPath, if non-empty, short-circuits Phase 2's master-source
	// choice: the card is loaded and its holder becomes the new
	// mind-form's master, regardless of any local identity.
	MasterCardPath string
	// OperatorKeyPath, if non-empty, supplies the nsec for Phase 1's
	// import branch from a file rather than interactive paste. The file
	// must contain either an `nsec1...` NIP-19 string or a 64-char hex.
	OperatorKeyPath string
}

// Run drives the wizard end-to-end. Returns the rendered Summoning
// (the in-memory state — never persisted) plus the response body the
// agent wrote at journal/0000-response.md. The cmd-level caller passes
// the body through r.Typewriter and prints a one-line completion.
func Run(ctx context.Context, d Deps) (*Summoning, []byte, error) {
	s := &Summoning{}
	initialized, err := isIdentityInitialized(d.StateDir)
	if err != nil {
		return nil, nil, err
	}
	s.Subsequent = initialized

	if !initialized {
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

	// Subsequent run: load local master default, skip phase 0/1.
	if err := loadLocalMasterDefault(d.StateDir, s); err != nil {
		return s, nil, err
	}
	d.Renderer.Show(fmt.Sprintf(stringFor(s.Lang, "welcome_back"), s.MasterLabel))
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

// IsIdentityInitialized reports whether <stateDir>/key and
// <stateDir>/state.db both exist. This is the single predicate
// consulted by both cmd/eidos/main.go's auto-dispatch and the
// wizard's own subsequent-mode branch — they cannot drift.
func IsIdentityInitialized(stateDir string) bool {
	v, _ := isIdentityInitialized(stateDir)
	return v
}

func isIdentityInitialized(stateDir string) (bool, error) {
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

// loadLocalMasterDefault reads the local gate's identity into s as the
// default master. Phase 2 may overwrite these fields if the user picks
// a card-as-master path. Used only on subsequent runs (when local
// identity exists by precondition).
func loadLocalMasterDefault(stateDir string, s *Summoning) error {
	k, err := identity.LoadKey(filepath.Join(stateDir, "key"))
	if err != nil {
		return fmt.Errorf("load operator key: %w", err)
	}
	s.MasterNpub = k.Npub
	s.OperatorPresent = true

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
	s.MasterLabel = label
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
