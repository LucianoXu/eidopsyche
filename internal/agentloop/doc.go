//go:build !windows

// Package agentloop owns the long-lived per-mind-form claude subprocess
// that holds a stream-json session open across many wakes. It reads
// wake.Signal JSONL from its parent's stdin (supervisor's Forward
// callback), renders each into a Claude Code user message via
// internal/prompts.BuildWake, writes them to claude's stdin, and reads
// claude's stream-json stdout to maintain a busy/idle state machine
// and per-turn transcripts.
//
// Dream rotation: agent-loop fsnotify-watches /eidos/run/dream-state.json.
// On dream-end transitions it waits for claude to reach idle (current
// turn's `result` event), closes claude's stdin, mints a new session
// UUID, and spawns a fresh claude with --session-id.
//
// See docs/superpowers/specs/2026-05-12-mindform-always-on-design.md.
package agentloop
