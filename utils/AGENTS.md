# Agent notes for `utils/`

Dev-only Go utilities. Not shipped, not in the root `go.work`, not built by CI.
See `README.md` for the full policy and each utility's own README for usage.

## The trap

Each utility is its own Go module that lives **outside** the root workspace.
A plain `go run .` fails with `main module ... does not contain package ...`
because Go finds the parent `go.work` and refuses. Invocation must set
`GOWORK=off`:

```bash
GOWORK=off go run .                   # from inside a utility dir
GOWORK=off go -C utils/<name> run .   # from the repo root
```

`GOWORK=off` is required, not optional.

## Adding a utility

New subdirectory with its own `go.mod`. Do **not** add it to the root
`go.work` — if that's tempting, it isn't a utility and belongs under
`internal/` instead. No stability contract: utilities may be deleted at any
time.

## Current contents

- `promptdump/` — captures Claude Code's Anthropic Messages API request body
  so the verbatim default system prompt can be studied. See its README.
