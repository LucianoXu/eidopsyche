package firstcontact

import "time"

// CharacterProfile is the dramaturge-research output: features only,
// not a name-and-source identification. Used as input to the
// "displaying" prompt. Sources are recorded for debug; the wizard
// never shows them to the operator.
type CharacterProfile struct {
	Archetype   string   `json:"archetype"`
	Temperament string   `json:"temperament"`
	World       string   `json:"world"`
	Settings    []string `json:"settings"`
	Imagery     []string `json:"imagery"`
	Sources     []string `json:"sources"`
}

// Summoning is the in-memory state machine for one ritual. Fields are
// populated phase-by-phase. The struct is never persisted; if Run()
// returns before reaching the end, all of these fields are discarded
// (except what Phase 1's identity.Bootstrap already committed to disk).
//
// Master vs Operator: the *master* is the new mind-form's owner — what
// gets burned into chest/summoning-book.md and forge.CreateOpts.Owner.
// The *operator* is the local user of the host running this wizard.
// In the simple path they are the same identity. They differ only when
// Phase 2 picks a card-as-master path (the local operator hosts a
// mind-form whose master lives elsewhere). OperatorPresent records
// whether a local identity exists at all on this host (Phase 1's
// 跳过 branch produces OperatorPresent=false).
type Summoning struct {
	Lang            string
	OperatorPresent bool
	MasterLabel     string
	MasterNpub      string
	HomeRelay       string // master's home relay; doubles as mind-form's per spec § 4.4.2
	CharacterPrompt string
	Profile         CharacterProfile
	Displaying      string
	RoleResearch    string // rich dramaturge dossier; written to self/role-research.md
	SummonedName    string
	Slug            string
	MindFormNpub    string
	MindFormKeyHex  string
	CallingWords    string
	StartedAt       time.Time
	Subsequent      bool

	// PrefabID is set by Phase 3's prefab branch; empty on the scratch
	// path. Phase 4 inspects it to choose the tar source and to skip
	// claude-driven calling-words generation.
	PrefabID string

	// HeartbeatInterval is the cadence collected by Phase 3.5 (heart
	// cadence). Empty means "use system default" (the supervisor
	// falls back to config.DefaultHeartbeatInterval at PID-1 startup).
	// Passed into forge.CreateOpts.HeartbeatInterval at seal time so
	// init-volume stamps it into the freshly-written config.toml.
	HeartbeatInterval string
}
