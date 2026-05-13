package prompts

import (
	"fmt"
	"os"
	"path/filepath"
)

// IdentityFacts are the machine-readable identity fields injected
// into the mind-form's system prompt at every spawn. They are
// immutable per mind-form once set at birth; the mind-form has
// no tool that should rewrite them, and the framework rebuilds
// them from disk (self/identity.md frontmatter) on each spawn.
type IdentityFacts struct {
	Label        string // e.g. "alice"
	MindFormNpub string // own Nostr public key
	OwnerNpub    string // master's Nostr public key
	OwnerLabel   string // master's chosen label, e.g. "Bob"
	CreatedDate  string // YYYY-MM-DD
	Kind         string // "m" / "f" / "spirit" / ""
	PrefabID     string // prefab dir id; "" for blank summons
	HomeRelay    string // ws(s)://… ; "" if not set
	Model        string // claude model id at spawn time
	OntologyDir  string // mount point inside the container
}

// FromOntology reads self/identity.md from ontologyDir and parses
// its frontmatter into IdentityFacts. Model and OntologyDir are
// not populated — callers (the agent-loop) supply those from
// daemon config at spawn time.
func FromOntology(ontologyDir string) (IdentityFacts, error) {
	body, err := os.ReadFile(filepath.Join(ontologyDir, "self", "identity.md"))
	if err != nil {
		return IdentityFacts{}, fmt.Errorf("read self/identity.md: %w", err)
	}
	fm, _, err := ParseFrontmatter(string(body))
	if err != nil {
		return IdentityFacts{}, fmt.Errorf("parse self/identity.md frontmatter: %w", err)
	}
	return IdentityFacts{
		Label:        fm["label"],
		MindFormNpub: fm["mindform_npub"],
		OwnerNpub:    fm["owner_npub"],
		OwnerLabel:   fm["owner_label"],
		CreatedDate:  fm["created_date"],
		Kind:         fm["kind"],
		PrefabID:     fm["prefab"],
		HomeRelay:    fm["home_relay"],
	}, nil
}
