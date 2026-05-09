package firstcontact

const (
	// ClaudeModel is the model id pinned for all dramaturge calls during
	// the wizard. Bump this constant when a newer Sonnet is preferred —
	// the wizard's literary register is calibrated for Sonnet, not Opus
	// (faster, cheaper, sufficient).
	ClaudeModel = "claude-sonnet-4-6"

	// PublicHomeRelay is offered as the default home-relay choice in
	// phase 1. Until the project ships its own community relay, the
	// constant points at a well-known public Nostr relay.
	PublicHomeRelay = "wss://relay.damus.io"

	// TypewriterCPS is the per-character pacing for typewriter-style
	// rendering of claude-generated paragraphs. ~30 chars/sec.
	TypewriterCPS = 30
)
