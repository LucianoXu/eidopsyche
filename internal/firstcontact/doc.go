// Package firstcontact implements the First Contact wizard — the
// one-shot ritual that converts "downloaded eidos" into "running
// MindForm".
//
// The wizard is invoked from two places in cmd/eidos:
//
//   - bare `eidos` when no <state-dir>/state.db exists yet
//   - `eidos summon` (always; subsequent runs of the wizard for a 2nd
//     MindForm)
//
// All in-flight state lives in a Summoning struct on the goroutine
// stack of Run(); a Ctrl-C, network drop, or claude failure discards
// everything except whatever phase 1 already committed (the operator's
// identity files, which are reusable and intentionally retained).
//
// The wizard relies on two sanctioned bootstrap exceptions — direct
// calls to internal helpers without going through the daemon's
// methodTable — because the daemon is guaranteed to be down at the
// moment those calls happen:
//
//  1. Phase 1 calls identity.Bootstrap to materialise the operator's
//     gate state. There is no daemon to dispatch through; this is
//     always the first write to the state directory.
//  2. Phase 3 calls forge.Orchestrate directly. Container creation does
//     not flow through the daemon today (the daemon does not own
//     forge.create as an IPC method), so the wizard wraps the existing
//     direct-to-Docker code path.
//
// Both exceptions are documented at their respective call sites and in
// docs/superpowers/specs/2026-05-09-first-contact-wizard-design.md §3.
package firstcontact
