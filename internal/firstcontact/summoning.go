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
// (except what phase 1 already wrote to disk via identity.Bootstrap).
type Summoning struct {
	Lang            string
	OperatorLabel   string
	OperatorNpub    string
	HomeRelay       string
	CharacterPrompt string
	Profile         CharacterProfile
	Displaying      string
	SummonedName    string
	Slug            string
	MindFormNpub    string
	MindFormKeyHex  string
	CallingWords    string
	StartedAt       time.Time
	Subsequent      bool
}
