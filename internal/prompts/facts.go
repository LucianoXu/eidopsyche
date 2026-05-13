package prompts

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// IdentityFacts are the machine-readable identity fields injected
// into the mind-form's system prompt at every spawn. They are
// immutable per mind-form once set at birth; the mind-form has
// no tool that should rewrite them, and the framework rebuilds
// them from disk (self/identity.toml) on each spawn.
//
// Model, Effort, and OntologyDir are populated by callers from daemon
// config at spawn time, not from the on-disk file.
type IdentityFacts struct {
	Label        string // e.g. "alice"
	MindFormNpub string // own Nostr public key
	OwnerNpub    string // creator's Nostr public key
	OwnerLabel   string // creator's chosen label, e.g. "Bob"
	CreatedDate  string // YYYY-MM-DD
	Model        string // claude model id at spawn time
	Effort       string // reasoning effort: low | medium | high
	OntologyDir  string // mount point inside the container
}

// identityTOML mirrors the on-disk self/identity.toml schema. Fields
// the framework writes at birth; the mind-form treats the file as
// read-only by convention.
type identityTOML struct {
	Label        string `toml:"label"`
	MindGateNpub string `toml:"mindgate_npub"`
	CreatorNpub  string `toml:"creator_npub"`
	CreatorLabel string `toml:"creator_label"`
	CreatedDate  string `toml:"created_date"`
}

// IdentityFile is the canonical relative path within an ontology root
// where identity facts are persisted.
const IdentityFile = "self/identity.toml"

// FromOntology reads self/identity.toml from ontologyDir and parses
// it into IdentityFacts. Model, Effort, and OntologyDir are not
// populated — callers (the agent-loop) supply those from daemon
// config at spawn time.
func FromOntology(ontologyDir string) (IdentityFacts, error) {
	path := filepath.Join(ontologyDir, IdentityFile)
	body, err := os.ReadFile(path)
	if err != nil {
		return IdentityFacts{}, fmt.Errorf("read %s: %w", IdentityFile, err)
	}
	var parsed identityTOML
	if _, err := toml.Decode(string(body), &parsed); err != nil {
		return IdentityFacts{}, fmt.Errorf("parse %s: %w", IdentityFile, err)
	}
	return IdentityFacts{
		Label:        parsed.Label,
		MindFormNpub: parsed.MindGateNpub,
		OwnerNpub:    parsed.CreatorNpub,
		OwnerLabel:   parsed.CreatorLabel,
		CreatedDate:  parsed.CreatedDate,
	}, nil
}
