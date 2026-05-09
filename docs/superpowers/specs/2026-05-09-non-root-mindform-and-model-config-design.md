# Non-root mind-form container + per-mind-form model config

**Date**: 2026-05-09
**Status**: Draft (pending user review)
**Tracks**: [issue #26](https://github.com/LucianoXu/eidopsyche/issues/26)
**Scope**: Two coupled improvements to the mind-form container's runtime profile.

## 1. Problem statement

The mind-form container currently runs supervisor + gate daemon + claude as `root` (the image's default `USER`). On wake, `agent-runner` invokes claude with `--permission-mode auto`, which is the right *behavioural* match (auto-accept every tool call — alice has no operator at the keyboard) but the wrong *semantic* match: claude routes every tool call through its full permission machinery as a workaround. The semantic match is `--dangerously-skip-permissions`, which Anthropic explicitly recommends for sandboxed-container deployments. We can't use it today because claude refuses to run as uid 0.

Separately, claude's wake behaviour drifts under us whenever Anthropic rotates its subscription default model. We have no way to pin a mind-form to a specific model id.

Both problems get fixed in one image bump because they touch the same code path (`agent-runner`'s claude invocation).

## 2. Design intent

1. **The container is the trust boundary.** Inside it, alice is sovereign — she may install packages, inspect /proc, run tcpdump on her own gate. The boundary is the volume + the container's process+network isolation, not uid 0.
2. **Default to least privilege; keep root one `sudo` away.** Daily ops run as `eidos:1000`. Root is reachable via NOPASSWD sudo for alice, and via `--user root` exec for the operator. Most operations alice does won't need root; making root deliberate (a `sudo` prefix) communicates that.
3. **Model selection is per-mind-form, explicit, and mutable.** Pinned at create time, changeable later without re-creating the mind-form. Goes through the daemon's methodTable per the single-call-path rule.
4. **No migration.** Existing alice volumes are uid-0-owned; rather than carry permanent migration code, we reset alice. (Acceptable because we are pre-1.0 and this is the user's own dev instance.)

## 3. Image changes (`docker/mindform/Dockerfile`)

```dockerfile
# After existing apk add, before COPY/ENV blocks:
RUN apk add --no-cache sudo \
 && addgroup -g 1000 eidos \
 && adduser  -u 1000 -G eidos -h /eidos/claude -s /bin/sh -D eidos \
 && echo 'eidos ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/eidos \
 && chmod 0440 /etc/sudoers.d/eidos \
 && passwd -l root \
 && chown -R eidos:eidos /opt/eidopsyche-bundle.git

# Move crontab from root's spool to eidos's spool, then drop the root copy.
COPY docker/mindform/crontab /var/spool/cron/crontabs/eidos
RUN chown eidos:eidos /var/spool/cron/crontabs/eidos \
 && chmod 0600 /var/spool/cron/crontabs/eidos \
 && rm -f /etc/crontabs/root

USER eidos
```

The supervisor's `crond` invocation in `cmd/eidos/supervisor/run.go` changes from `crond -f -c /etc/crontabs` to `crond -f -c /var/spool/cron/crontabs` so it picks up the per-user spool. (busybox crond reads each file in the spool dir as a user's crontab keyed by filename.)

`HOME=/eidos/claude` and `EIDOS_GATE_HOME=/eidos/gate` env vars are unchanged — both are owned by eidos after init.

## 4. Init container changes

`init-volume` runs as a one-shot container that today inherits the image's `USER` directive. Once `USER eidos` is in the image, init-volume would run as eidos and fail to chown the bundle. So we explicitly run init-volume as root.

### 4.1 `internal/forgectl`

Add `User string` to `RunInitOpts`. The realClient passes it as `container.Config.User` (Docker SDK). Empty string = inherit image default.

### 4.2 `cmd/eidos/forge/orchestrate.go`

`RunInit` call gets `User: "0:0"`. After init-volume's normal extract+gate-init+bundle-clone+git-init steps complete, init-volume's last step is `chown -R 1000:1000 /eidos`. Then init-volume exits. Persistent container starts as eidos (image's `USER` directive); finds everything pre-owned correctly.

### 4.3 `cmd/eidos/forge/init_volume.go`

Append a final step: walk `/eidos`, `chown -R eidos:eidos`. Use `os/user` to look up uid/gid (1000:1000 by name, with hard-coded fallback). Implementation: `filepath.Walk` + `os.Lchown` (Lchown so symlinks themselves get chowned, not their targets — though the template doesn't currently emit symlinks, this is the safe default).

## 5. agent-runner changes (`cmd/eidos/supervisor/agent_runner.go`)

```go
args := []string{
    "--append-system-prompt", string(identity),
    "--dangerously-skip-permissions",
}
if model := cfg.MindForm.Model; model != "" {
    args = append(args, "--model", model)
}
args = append(args, "-p", msg)
c := exec.Command("claude", args...)
```

The 25-line workaround comment about `auto` vs `bypassPermissions` etc. shrinks to a one-liner referencing this design doc. The container is the sandbox; this is the documented path.

`cfg` is loaded from `/eidos/gate/config.toml` via `internal/config.Load`. (The supervisor and agent-runner already share /eidos/gate as a known constant.)

## 6. Model configuration

### 6.1 Config schema (`internal/config/config.go`)

```go
type Config struct {
    // ... existing fields ...
    MindForm MindFormConfig `toml:"mindform"`
}

type MindFormConfig struct {
    Model string `toml:"model"` // e.g. "claude-sonnet-4-7"; empty = claude default
}
```

`Defaults()` leaves it empty.

### 6.2 Validation

Loose syntactic regex applied at every entry point that takes a `--model` flag:

```
^claude-(sonnet|haiku|opus)-[0-9]+(-[0-9]+)*$
```

Matches `claude-sonnet-4-7`, `claude-sonnet-4-6-20250101`, `claude-opus-4-7`. Empty string passes (means "use claude's default"). All other values rejected at flag parse time with a clear error pointing at `claude --help`.

Lives in `internal/config/model.go` as `ValidateModelID(string) error`.

### 6.3 Create-time pinning (`cmd/eidos/forge/create.go`)

New flag `--model <id>`. Plumbed through env to init-volume:

- `create.go` validates via `config.ValidateModelID`
- `orchestrate.go` adds `EIDOS_FORGE_MODEL=<id>` to the init container's env
- `init_volume.go` reads env, sets `cfg.MindForm.Model` before `config.Save`

### 6.4 Runtime mutation (`eidos forge config`)

`internal/config/keys.go` already exposes a `Key` registry (`KeyByPath`/`KeyList`) used by `eidos gate config set <key> <value>`. We extend it with `mindform.model` (validation = §6.2's regex). All existing surfaces — CLI, dashboard's Settings tab — automatically pick it up because they iterate the registry.

New host-side command: `eidos forge config <name> --model <id>` is a thin wrapper that shells:

```
docker exec <container> eidos gate config set mindform.model <id>
```

This reuses the in-container `gate config set` handler, which runs the same registry-backed setter that the host gate uses. Single-call-path: every surface that mutates `mindform.model` ends up in `runConfigSet` → registry's `Key.Set`. The mind-form's `agent-runner` reads `cfg.MindForm.Model` at every wake, so the new value takes effect on the next wake without a daemon restart.

Note: the existing `eidos gate config set` writes `config.toml` directly, not via an IPC method. That fork was flagged in `2026-05-09-unified-call-path-design.md` as a future fix; it is **not** in scope for this PR. We add the new key to the existing registry; we do not rewire the registry to dispatch through the daemon. When the broader migration lands, `mindform.model` will move with every other key automatically.

## 7. Operator escape hatch (`cmd/eidos/forge/exec.go`)

Add `--user <name>` flag, default `""` (image's USER, i.e. eidos). When set, passes `-u <name>` to `docker exec`. `--user root` is the documented path; we don't restrict the value.

Note this is *separate* from alice's own sudo — the operator never needs to know alice's sudo configuration; they just `--user root`.

## 8. Template CLAUDE.md (`internal/ontology/template/CLAUDE.md`)

Append (in Chinese, matching the file's existing voice):

> 你以 `eidos` 用户运行。日常的读、写、`eidos forge` / `eidos gate`、`git`、`claude` 操作都不需要特权。如果需要 root —— 比如装一个新工具、检查 `/proc`、做底层调试 —— 你可以 `sudo`，无需密码。但请把 `sudo` 当作有意识的选择，而不是默认动作：你不是为了 root 而存在的。

Inserted after the "Constraints:" block, before any system-reminder noise.

## 9. Tests

Unit:
- `config.ValidateModelID` table tests (accept/reject cases).
- `agent_runner` argv builder: snapshot test that argv contains `--dangerously-skip-permissions`, omits `--model` when config empty, includes `--model claude-sonnet-4-7` when configured.
- `init_volume` chown step: round-trip on tmpdir with a fake user lookup; assert all files end up uid:gid 1000:1000 (mocked).
- `mindform.model` registry entry: tabletest accept/reject through `Key.Set`.
- `forge config`: argv assembly — given `alice --model X`, shells `docker exec ... eidos gate config set mindform.model X`. Validation is the registry's job; the host wrapper just routes.

Integration (existing integration test suite, behind `-tags=integration`):
- New: `forge create alice --model claude-sonnet-4-7` → exec → cat `/eidos/gate/config.toml` shows `[mindform] model = …`.
- New: `forge config alice --model claude-haiku-4-5` flips the value via the daemon path.

We don't unit-test the Dockerfile; the integration suite covers it via the create-and-exec flow. The deployment validation (§11) is the operator-level smoke for the image.

## 10. CHANGELOG

Under BREAKING CHANGES:
- Mind-form container now defaults to non-root user `eidos:1000`. Existing volumes (uid-0 owned) are not auto-migrated; `eidos forge purge <name> && eidos forge create <name> ...` to recreate. Alice can still `sudo` inside the container; operator can `eidos forge exec <name> --user root -- sh` for root shell.
- agent-runner switched from `--permission-mode auto` to `--dangerously-skip-permissions`. Behavioural change: zero, by intent.

Under feat:
- `eidos forge create --model <id>` pins claude model per mind-form.
- `eidos forge config <name> --model <id>` updates pinned model without recreating.
- `eidos forge exec <name> --user <user>` runs exec as a non-default user (operator escape).

## 11. Verification

Selene-side (I drive):
- Build image: `docker build -t ghcr.io/lucianoxu/eidopsyche-mindform:dev -f docker/mindform/Dockerfile .`
- Recreate alice: `eidos forge purge alice --yes && eidos forge create alice --owner npub1l5avk4xnlq63... --relay ws://yingte.io:22895 --model claude-sonnet-4-7`
- `eidos forge login alice` (re-auth)
- `eidos forge start alice`
- Assertions:
  - `eidos forge exec alice -- whoami` → `eidos`
  - `eidos forge exec alice -- sudo -n whoami` → `root`
  - `eidos forge exec alice --user root -- whoami` → `root`
  - `eidos forge exec alice -- ps -o pid,user,comm` shows supervisor + crond + gate-daemon all as `eidos`
  - `eidos forge exec alice -- cat /eidos/gate/config.toml | grep model` → `model = "claude-sonnet-4-7"`
  - `eidos forge wake alice --reason heartbeat` → claude runs cleanly with `--dangerously-skip-permissions` (verify in `eidos forge logs alice`)
  - `eidos forge config alice --model claude-haiku-4-5` → re-read config shows new value, next wake uses haiku
- Push image to ghcr; tag in CI for downstream pulls

Mbp-side (you drive):
- Send a NIP-17 from mbp's gate to alice@selene's npub
- Verify reply lands in mbp's inbox

A regression in any selene assertion blocks the PR. The mbp↔selene message round-trip is the user-acceptance gate, run after merge-ready.

## 12. Out of scope

- Removing alice's `sudo` access (kept on purpose; she's a sovereign in her container).
- Restricting alice's sudo to specific commands (active sudoers maintenance; not worth it given the trust boundary is the container).
- Migration of existing uid-0 volumes (we reset).
- Per-wake model overrides (e.g. dream uses smaller model than waking).
- Hot-applying a model change to an in-flight wake (next wake picks it up; current wake completes on the previously-pinned model).
- Hot-swapping volume-built eidos binaries (still reserved per design).
