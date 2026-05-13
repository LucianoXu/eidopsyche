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
	"github.com/LucianoXu/eidopsyche/internal/ontology"
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

	// EnsureSummonReady is called after Phase 2 returns a summon action
	// (Local or Card) and before Phase 2.5 starts. The cmd-side uses
	// it to defer docker-side prerequisites — daemon connection,
	// volume writers — until the user has actually committed to
	// summoning. This is what makes the mindgate-only flow (Phase 2
	// = Exit) reachable on hosts that don't have docker installed.
	//
	// Claude on PATH is intentionally NOT validated here; see
	// EnsureClaudeReady below.
	//
	// The callback receives a pointer to the Deps so it can populate
	// DockerClient / WriteVolume / StartContainer / ResponseWait /
	// AddContact / ExistingSlugs / Image / ReadyDeps in place. nil
	// callback = the caller has already populated those fields (test
	// path).
	EnsureSummonReady func(*Deps) error

	// EnsureClaudeReady is called after Phase 2.5 returns
	// ScaffoldScratch, and before Phase 3's claude-driven flow runs.
	// The cmd-side uses it to defer claude-on-PATH validation until
	// the operator commits to the scratch path — so an operator
	// picking a prefab on a host without claude installed can still
	// proceed. nil callback = caller already populated d.Claude.
	EnsureClaudeReady func(*Deps) error
}

// Run drives the wizard end-to-end through the four-phase tree:
//
//	Phase 0 (open)    — first run: logo + language + project intro
//	Phase 1 (identity) — first run: 新建 / 导入 / 跳过
//	Phase 2 (action)   — every run: exit / summon-with-local / summon-with-card
//	Phase 3 (book)     — when summoning: character + research + display + name
//	Phase 4 (seal)     — when summoning: book preview + calling-words + birth
//
// Returns the rendered Summoning (in-memory state, never persisted)
// plus the response body the agent wrote at chest/first-words.md.
// On Phase2Exit, body is nil and err is nil — the wizard ends cleanly.
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
		if err := Phase1(ctx, s, d.Renderer, Phase1Deps{
			StateDir:        d.StateDir,
			OperatorKeyPath: d.OperatorKeyPath,
		}); err != nil {
			if errors.Is(err, ErrSelfHostExit) {
				return s, nil, ErrSelfHostExit
			}
			return s, nil, err
		}
	} else {
		if err := loadLocalMasterDefault(d.StateDir, s); err != nil {
			return s, nil, err
		}
		d.Renderer.Show(fmt.Sprintf(stringFor(s.Lang, "welcome_back"), s.MasterLabel))
	}

	action, err := Phase2(ctx, s, d.Renderer, Phase2Deps{
		EntryMode:      d.EntryMode,
		MasterCardPath: d.MasterCardPath,
	})
	if err != nil {
		return s, nil, err
	}
	if action == Phase2Exit {
		return s, nil, nil
	}

	// User committed to summoning. NOW resolve docker / volume helpers
	// (claude is resolved later, only on the scratch branch).
	if d.EnsureSummonReady != nil {
		if err := d.EnsureSummonReady(&d); err != nil {
			return s, nil, fmt.Errorf("summon prerequisites: %w", err)
		}
	}

	choice, err := Phase2Half(ctx, s, d.Renderer)
	if err != nil {
		return s, nil, err
	}
	if choice == ScaffoldExit {
		return s, nil, nil
	}

	ready := StartBackground(ctx, d.ReadyDeps)
	existing, _ := d.ExistingSlugs()

	if choice == ScaffoldPrefab {
		metas, listErr := ontology.List()
		if listErr != nil {
			return s, nil, fmt.Errorf("list prefabs: %w", listErr)
		}
		if err := Phase3Prefab(ctx, s, d.Renderer, Phase3PrefabDeps{
			Catalogue:     metas,
			ExistingSlugs: existing,
		}); err != nil {
			return s, nil, err
		}
	} else {
		if d.EnsureClaudeReady != nil {
			if err := d.EnsureClaudeReady(&d); err != nil {
				return s, nil, fmt.Errorf("claude prerequisites: %w", err)
			}
		}
		if err := Phase3(ctx, s, d.Renderer, d.Claude, Phase3BookDeps{ExistingSlugs: existing}); err != nil {
			return s, nil, err
		}
	}

	// Phase 3.5: heart cadence — ask both prefab and scratch paths
	// before sealing, so the cadence is baked into config.toml at
	// init-volume time rather than requiring a post-create restart.
	if err := Phase3Cadence(ctx, s, d.Renderer); err != nil {
		return s, nil, err
	}

	// Phase 3.6: calling-words review/edit. After cadence (config) and
	// before sealing (ceremony). Both prefab and scratch paths produce
	// a default and let the operator accept or edit. Empty allowed.
	if err := Phase3CallingWords(ctx, s, d.Renderer, d.Claude); err != nil {
		return s, nil, err
	}

	body, err := Phase4(ctx, s, d.Renderer, d.Claude, ready, Phase4Deps{
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
