# `utils/` — dev-only utilities

This directory holds standalone Go utilities used during eidopsyche
development. They are **not** part of the shipped `eidos` binary.

Policy:

- Each utility is its own Go module with its own `go.mod`.
- `utils/` is **not** included in the root `go.work` file. The root
  workspace's `go build ./...` / `go test ./...` do not touch utilities,
  and adding a dependency here does not bloat the `eidos` import graph.
- Utilities are invoked explicitly, e.g. `cd utils/promptdump && go run .`
  or `go run ./utils/promptdump` from inside the utility's own module.
- Utilities are not built in CI by default and are not released. Authors
  add their own CI job if they want one.
- Utilities may be deleted at any time — there is no stability contract.
