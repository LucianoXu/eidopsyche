// Package transcript captures and stores Claude Code's stream-json event
// stream for one wake. It is the shared data layer for:
//
//   - the agent-runner harness (writes events as claude emits them, builds
//     a per-wake metadata entry for the index, runs rotation),
//   - in-container `forge transcript-list` / `forge transcript-tail` (read
//     events back out for the host renderer),
//   - the host `forge watch` renderer (parses the same NDJSON shape).
//
// The on-disk layout per spec
// docs/superpowers/specs/2026-05-10-mindform-status-and-observer-design.md
// §5:
//
//	transcripts/
//	├── index.json                  # metadata, sorted newest-first
//	├── current → wake-<id>.ndjson  # symlink, present only during a wake
//	└── wake-<id>.ndjson            # one file per wake, append-only NDJSON
//
// Concurrency model: agent-runner is single-instance (enforced by
// /eidos/run/agent.lock), so the Store has exactly one writer and needs
// no internal locking. Readers (transcript-tail) read independently and
// tolerate the writer being mid-write — a partial trailing line is
// expected and skipped at line boundaries.
package transcript
