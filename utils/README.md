# `utils/` — dev-only utilities

This directory holds standalone Go utilities used during eidopsyche
development. They are **not** part of the shipped `eidos` binary.

Policy:

- Each utility is its own Go module with its own `go.mod`.
- `utils/` is **not** included in the root `go.work` file. The root
  workspace's `go build ./...` / `go test ./...` do not touch utilities,
  and adding a dependency here does not bloat the `eidos` import graph.
- Utilities are invoked explicitly with `GOWORK=off` (because the
  utility's module is intentionally outside the root workspace —
  without the flag, Go finds the parent `go.work` and refuses with
  "main module ... does not contain package ..."). From inside the
  utility's own dir: `GOWORK=off go run .`. From the repo root:
  `GOWORK=off go -C utils/<name> run .`. See each utility's own
  README for examples.
- Utilities are not built in CI by default and are not released. Authors
  add their own CI job if they want one.
- Utilities may be deleted at any time — there is no stability contract.
