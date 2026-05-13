// Package promptcapture intercepts the Anthropic Messages API request body
// Claude Code sends, so callers can inspect the verbatim default system
// prompt, tool catalogue, and first user message under arbitrary claude
// flag combinations.
//
// The package is shared by two surfaces:
//
//   - cmd/eidos/forge.prompt-dump (the in-container worker invoked by
//     "eidos forge prompt-dump <name>" against a running mind-form).
//   - utils/promptdump (the dev-only host shim, kept outside go.work).
//
// Both surfaces drive promptcapture.Run, which:
//
//  1. binds a loopback HTTP server on 127.0.0.1:0,
//  2. spawns claude with ANTHROPIC_BASE_URL=http://127.0.0.1:<port> and a
//     dummy API key,
//  3. captures the first POST /v1/messages body,
//  4. returns a minimal-valid SSE response so claude exits cleanly,
//  5. assembles a typed Envelope with metadata + parsed request body.
//
// See docs/superpowers/specs/2026-05-12-forge-prompt-dump-design.md for
// the design rationale and the alternatives considered.
package promptcapture
