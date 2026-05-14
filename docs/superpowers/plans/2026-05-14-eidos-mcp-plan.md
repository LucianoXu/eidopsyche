# eidos-mcp Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a new `eidos mcp` subcommand that exposes 21 curated IPC methods of the gate daemon as typed MCP tools over stdio, so the mind-form's claude session (and optionally the operator's host claude) can act on MindForge framework state through a typed tool surface instead of shelling out to `eidos gate ...`.

**Architecture:** Thin adapter pattern. `cmd/eidos/mcp` is a cobra entry that resolves the gate socket from `internal/config`, builds an `internal/mcp.Server`, and runs the official `github.com/modelcontextprotocol/go-sdk` over `mcp.StdioTransport{}`. Tool handlers in `internal/mcp/tools_*.go` declare typed `Input`/`Output` structs (schema is generated from `jsonschema:"..."` tags by the SDK), call into the daemon via `internal/ipc.Client.Call`, and propagate daemon errors as `[CODE] message` text errors. Same binary serves both container (dials `/eidos/gate/sock`) and host (dials `$EIDOS_GATE_HOME/sock`) contexts; the daemon's `Context` bitmask gates which writes are accepted.

**Tech Stack:** Go 1.25, cobra, `github.com/modelcontextprotocol/go-sdk` (Apache 2.0 / MIT), existing `internal/ipc`, `internal/config`, `internal/envelope`, `internal/version` packages.

**Spec:** [`docs/superpowers/specs/2026-05-14-eidos-mcp-design.md`](../specs/2026-05-14-eidos-mcp-design.md)

---

## Convention used in this plan

Each tool handler in `internal/mcp/tools_*.go` follows this shape (the official SDK is generic over the typed `Input`/`Output` returned from the handler):

```go
type FooInput struct {
    Bar string `json:"bar" jsonschema:"what bar is"`
}
type FooOutput struct {
    Baz string `json:"baz"`
}

func (s *Server) eidosFoo(ctx context.Context, _ *mcp.CallToolRequest, in FooInput) (*mcp.CallToolResult, FooOutput, error) {
    var out FooOutput
    if err := s.client.Call(ctx, "foo.method", in, &out); err != nil {
        return nil, FooOutput{}, err
    }
    return nil, out, nil
}
```

The `s.client.Call` returns `error` (not `(*ipc.Error, error)`) — the wrapper in `internal/mcp/client.go` (Task 3) collapses the two into one `error` with the `[CODE] message` shape.

The unit test pattern uses a fake client recorder (also introduced in Task 3) that captures `(method, params)` and returns a canned `(result, err)`.

---

## Task 1: Add SDK dependency and verify import

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the official MCP Go SDK**

Run:
```bash
cd /data/eidopsyche
go get github.com/modelcontextprotocol/go-sdk@v1.6.0
```

- [ ] **Step 2: Verify it tidied into the workspace**

Run:
```bash
grep modelcontextprotocol/go-sdk go.mod
```
Expected: a `require github.com/modelcontextprotocol/go-sdk v1.6.0` line (or newer if the registry has bumped between brainstorming and now — any v1.x is fine).

- [ ] **Step 3: Smoke-test import (no commit yet)**

Create a temporary file `/tmp/mcp_smoke.go`:
```go
package main

import (
    "fmt"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
    s := mcp.NewServer(&mcp.Implementation{Name: "smoke", Version: "0.0.0"}, nil)
    fmt.Println(s)
}
```

Run:
```bash
cd /data/eidopsyche
go run /tmp/mcp_smoke.go
rm /tmp/mcp_smoke.go
```
Expected: prints a non-nil `*mcp.Server` and exits 0. No compile errors.

- [ ] **Step 4: Run go mod tidy and commit**

Run:
```bash
go mod tidy
git add go.mod go.sum
git commit -m "build(mcp): add modelcontextprotocol/go-sdk dependency"
```

---

## Task 2: Scaffold subcommand and internal package

**Files:**
- Create: `cmd/eidos/mcp/cmd.go`
- Create: `internal/mcp/server.go`
- Modify: `cmd/eidos/main.go`

- [ ] **Step 1: Write the failing build test for the subcommand**

Create `cmd/eidos/mcp/cmd_test.go`:
```go
package mcp

import (
    "testing"

    "github.com/spf13/cobra"
)

func TestCommand_ReturnsRoot(t *testing.T) {
    c := Command()
    if c == nil {
        t.Fatal("Command() returned nil")
    }
    if c.Use != "mcp" {
        t.Fatalf("Use = %q, want %q", c.Use, "mcp")
    }
    if _, ok := any(c).(*cobra.Command); !ok {
        t.Fatalf("Command() type = %T, want *cobra.Command", c)
    }
}
```

- [ ] **Step 2: Run the test to verify it fails (package doesn't exist yet)**

Run:
```bash
cd /data/eidopsyche
go test ./cmd/eidos/mcp/...
```
Expected: build failure or `no test files`. Either way, we need to create the package.

- [ ] **Step 3: Create the internal/mcp scaffold**

Create `internal/mcp/server.go`:
```go
// Package mcp implements the eidos-mcp MCP server: a thin adapter that
// exposes a curated subset of the gate daemon's IPC method table as
// typed MCP tools over stdio. The full design lives at
// docs/superpowers/specs/2026-05-14-eidos-mcp-design.md.
package mcp

import (
    "context"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server is the eidos-mcp server. It owns the SDK server instance and
// an IPC client used by all tool handlers to talk to the gate daemon.
type Server struct {
    sdk    *mcp.Server
    client *Client
}

// New returns an unstarted Server bound to the given gate socket path.
// All tools are registered on the SDK server before this returns.
func New(socket, version string) (*Server, error) {
    client, err := newClient(socket)
    if err != nil {
        return nil, err
    }
    sdk := mcp.NewServer(&mcp.Implementation{
        Name:    "eidos",
        Version: version,
    }, nil)
    s := &Server{sdk: sdk, client: client}
    s.registerTools()
    return s, nil
}

// Run blocks until the stdio transport closes (claude exits or ctx
// cancels). It is the main loop for the `eidos mcp` subcommand.
func (s *Server) Run(ctx context.Context) error {
    return s.sdk.Run(ctx, &mcp.StdioTransport{})
}

// registerTools is filled in by the per-domain tools_*.go files via
// their init-time hooks. Kept as a single method so the per-domain
// files can add registrations without each touching server.go.
func (s *Server) registerTools() {
    // populated in tools_*.go
}

// Close releases the IPC connection. Called by the cobra cmd on exit.
func (s *Server) Close() error { return s.client.Close() }
```

Note: `newClient`, `Client`, and the per-domain `register*` helpers are filled in by later tasks. This task is just the skeleton.

- [ ] **Step 4: Create the cobra entry**

Create `cmd/eidos/mcp/cmd.go`:
```go
// Package mcp provides the `eidos mcp` subcommand: a stdio MCP server
// that exposes the gate daemon's IPC method table as typed tools for
// the mind-form's (or operator's) Claude Code session.
//
// See docs/superpowers/specs/2026-05-14-eidos-mcp-design.md.
package mcp

import (
    "context"
    "fmt"
    "os/signal"
    "path/filepath"
    "syscall"

    "github.com/spf13/cobra"

    "github.com/LucianoXu/eidopsyche/internal/config"
    "github.com/LucianoXu/eidopsyche/internal/mcp"
    "github.com/LucianoXu/eidopsyche/internal/version"
)

var stateDir string

var rootCmd = &cobra.Command{
    Use:           "mcp",
    Short:         "MCP server exposing the gate daemon's method table as typed tools",
    SilenceUsage:  true,
    SilenceErrors: true,
    RunE: func(cmd *cobra.Command, args []string) error {
        dir, err := config.ResolveStateDir(stateDir)
        if err != nil {
            return fmt.Errorf("resolve state dir: %w", err)
        }
        cfg, _ := config.Load(filepath.Join(dir, "config.toml"))
        socket := filepath.Join(dir, cfg.Daemon.Socket)

        s, err := mcp.New(socket, version.Version)
        if err != nil {
            return fmt.Errorf("init mcp server: %w", err)
        }
        defer s.Close()

        ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
        defer cancel()
        return s.Run(ctx)
    },
}

func init() {
    rootCmd.PersistentFlags().StringVar(&stateDir, "state-dir", "", "MindGate state directory (default: $EIDOS_GATE_HOME or platform default)")
}

// Command returns the root cobra.Command for `eidos mcp`.
func Command() *cobra.Command { return rootCmd }
```

- [ ] **Step 5: Wire the subcommand into the root binary**

Modify `cmd/eidos/main.go`. Add the import (alongside the existing `cmd/eidos/...` imports) and the `rootCmd.AddCommand` call.

Add to imports (line ~14 area, between `gate` and `relay`):
```go
    "github.com/LucianoXu/eidopsyche/cmd/eidos/mcp"
```

Add to `init()` (line 41 area, between `forge.Command()` and `supervisor.Command()` or wherever group ordering feels natural):
```go
    rootCmd.AddCommand(mcp.Command())
```

- [ ] **Step 6: Run the subcommand-shape test and confirm pass**

Run:
```bash
cd /data/eidopsyche
go test ./cmd/eidos/mcp/...
```
Expected: PASS. (`TestCommand_ReturnsRoot` runs against the registered subcommand.)

- [ ] **Step 7: Verify the binary picks up the new subcommand**

Run:
```bash
cd /data/eidopsyche
go build -o /tmp/eidos-mcp-smoke ./cmd/eidos
/tmp/eidos-mcp-smoke mcp --help
rm /tmp/eidos-mcp-smoke
```
Expected: prints the cobra help for `eidos mcp` including the `--state-dir` flag. The Run handler does not execute (because `--help` short-circuits cobra), so the missing daemon is not an issue.

- [ ] **Step 8: Commit**

```bash
git add cmd/eidos/main.go cmd/eidos/mcp/ internal/mcp/server.go
git commit -m "feat(mcp): scaffold eidos mcp subcommand and internal/mcp package"
```

---

## Task 3: IPC client wrapper with error translation and a test fake

**Files:**
- Create: `internal/mcp/client.go`
- Create: `internal/mcp/client_test.go`
- Create: `internal/mcp/testhelpers.go`

- [ ] **Step 1: Write the failing test for the error translation**

Create `internal/mcp/client_test.go`:
```go
package mcp

import (
    "context"
    "errors"
    "testing"

    "github.com/LucianoXu/eidopsyche/internal/ipc"
)

func TestClient_Call_TypedDaemonError(t *testing.T) {
    fake := &fakeIPC{
        errResp: &ipc.Error{Code: "CONTACT_NOT_FOUND", Message: `no contact with label "alice"`},
    }
    c := &Client{raw: fake}
    err := c.Call(context.Background(), "contact.remove", map[string]any{"label": "alice"}, nil)
    if err == nil {
        t.Fatal("Call returned nil, want error")
    }
    want := `[CONTACT_NOT_FOUND] no contact with label "alice"`
    if err.Error() != want {
        t.Fatalf("err = %q, want %q", err.Error(), want)
    }
}

func TestClient_Call_TransportError(t *testing.T) {
    fake := &fakeIPC{transportErr: errors.New("broken pipe")}
    c := &Client{raw: fake}
    err := c.Call(context.Background(), "send", nil, nil)
    if err == nil {
        t.Fatal("Call returned nil, want error")
    }
    if got := err.Error(); got != "[IPC_TRANSPORT] broken pipe" {
        t.Fatalf("err = %q, want %q", got, "[IPC_TRANSPORT] broken pipe")
    }
}

func TestClient_Call_RoundTripParams(t *testing.T) {
    fake := &fakeIPC{}
    c := &Client{raw: fake}
    _ = c.Call(context.Background(), "set-label", map[string]any{"label": "alice"}, nil)
    if fake.lastMethod != "set-label" {
        t.Fatalf("method = %q, want set-label", fake.lastMethod)
    }
    if got := fake.lastParams.(map[string]any)["label"]; got != "alice" {
        t.Fatalf("params.label = %v, want alice", got)
    }
}
```

- [ ] **Step 2: Create the test fake**

Create `internal/mcp/testhelpers.go`:
```go
package mcp

import (
    "encoding/json"

    "github.com/LucianoXu/eidopsyche/internal/ipc"
)

// fakeIPC is a test double for the rawIPC interface (see client.go). It
// records the most-recent Call's (method, params), and returns the
// canned (result, errResp, transportErr) tuple. Used by every
// tools_*_test.go in this package.
type fakeIPC struct {
    lastMethod   string
    lastParams   any
    result       any
    errResp      *ipc.Error
    transportErr error
}

func (f *fakeIPC) Call(method string, params any, result any) (*ipc.Error, error) {
    f.lastMethod = method
    f.lastParams = params
    if f.transportErr != nil {
        return nil, f.transportErr
    }
    if f.errResp != nil {
        return f.errResp, nil
    }
    if f.result != nil && result != nil {
        b, _ := json.Marshal(f.result)
        _ = json.Unmarshal(b, result)
    }
    return nil, nil
}

func (f *fakeIPC) Close() error { return nil }
```

- [ ] **Step 3: Run the test to verify it fails (no Client type yet)**

Run:
```bash
cd /data/eidopsyche
go test ./internal/mcp/...
```
Expected: build failure — `Client`, `rawIPC`, etc. undefined.

- [ ] **Step 4: Implement `internal/mcp/client.go`**

Create `internal/mcp/client.go`:
```go
package mcp

import (
    "context"
    "fmt"

    "github.com/LucianoXu/eidopsyche/internal/ipc"
)

// rawIPC is the slice of *ipc.Client that this package depends on.
// Defined as an interface so unit tests can substitute a fake.
type rawIPC interface {
    Call(method string, params any, result any) (*ipc.Error, error)
    Close() error
}

// Client wraps an IPC client and collapses the daemon's two error
// channels (typed *ipc.Error result vs. transport error) into a single
// `error` formatted as "[CODE] message" — the shape the MCP SDK
// turns into a CallToolResult{IsError:true} for the model.
type Client struct {
    raw rawIPC
}

func newClient(socket string) (*Client, error) {
    c, err := ipc.Dial(socket)
    if err != nil {
        return nil, fmt.Errorf("dial gate socket %s: %w", socket, err)
    }
    return &Client{raw: c}, nil
}

// Call invokes a daemon IPC method. `params` is JSON-marshalled by
// ipc.Client. `result` is populated from the response body when the
// method succeeds. Errors are returned as `[CODE] message` so the
// LLM sees a stable code prefix it can pattern-match on.
func (c *Client) Call(_ context.Context, method string, params any, result any) error {
    // Note: ipc.Client.Call does not yet take ctx. We expose ctx in
    // the signature so future cancellation can plug in without
    // touching every handler. For now ctx is accepted and ignored.
    ipcErr, err := c.raw.Call(method, params, result)
    if err != nil {
        return fmt.Errorf("[IPC_TRANSPORT] %s", err.Error())
    }
    if ipcErr != nil {
        return fmt.Errorf("[%s] %s", ipcErr.Code, ipcErr.Message)
    }
    return nil
}

// Close releases the underlying IPC connection.
func (c *Client) Close() error { return c.raw.Close() }
```

- [ ] **Step 5: Run the tests and verify pass**

Run:
```bash
cd /data/eidopsyche
go test ./internal/mcp/...
```
Expected: all three `TestClient_*` tests PASS.

- [ ] **Step 6: Run gofmt + go vet (CI parity)**

Run:
```bash
gofmt -l internal/mcp/ cmd/eidos/mcp/
go vet ./internal/mcp/... ./cmd/eidos/mcp/...
```
Expected: empty output from both (no formatting drift, no vet warnings).

- [ ] **Step 7: Commit**

```bash
git add internal/mcp/
git commit -m "feat(mcp): IPC client wrapper with error code translation and test fake"
```

---

## Task 4: `eidos_state_get` tool — establishes the per-tool pattern

**Files:**
- Create: `internal/mcp/tools_state.go`
- Create: `internal/mcp/tools_state_test.go`

This is the foundational read tool. Once it works, every subsequent tool follows the exact same shape.

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/tools_state_test.go`:
```go
package mcp

import (
    "context"
    "testing"
)

func TestEidosStateGet_PassesPath(t *testing.T) {
    fake := &fakeIPC{result: map[string]any{"label": "alice"}}
    s := &Server{client: &Client{raw: fake}}

    _, out, err := s.eidosStateGet(context.Background(), nil, StateGetInput{Path: "identity"})
    if err != nil {
        t.Fatalf("eidosStateGet: %v", err)
    }
    if fake.lastMethod != "state.get" {
        t.Fatalf("method = %q, want state.get", fake.lastMethod)
    }
    params := fake.lastParams.(StateGetInput)
    if params.Path != "identity" {
        t.Fatalf("params.path = %q, want identity", params.Path)
    }
    if out.Value == nil {
        t.Fatal("Value is nil, want non-nil")
    }
}

func TestEidosStateGet_PropagatesError(t *testing.T) {
    fake := &fakeIPC{errResp: pathNotFound("config.missing")}
    s := &Server{client: &Client{raw: fake}}
    _, _, err := s.eidosStateGet(context.Background(), nil, StateGetInput{Path: "config.missing"})
    if err == nil {
        t.Fatal("expected error, got nil")
    }
    if got := err.Error(); got != "[PATH_NOT_FOUND] config.missing" {
        t.Fatalf("err = %q", got)
    }
}
```

Add to `internal/mcp/testhelpers.go` (the existing file from Task 3):
```go
import "github.com/LucianoXu/eidopsyche/internal/ipc"

func pathNotFound(path string) *ipc.Error {
    return &ipc.Error{Code: ipc.ErrPathNotFound, Message: path}
}
```

(Existing imports stay; only add the helper at the bottom of the file.)

- [ ] **Step 2: Run to verify fail**

Run:
```bash
cd /data/eidopsyche
go test ./internal/mcp/... -run TestEidosStateGet
```
Expected: build failure — `eidosStateGet`, `StateGetInput`, `StateGetOutput` undefined.

- [ ] **Step 3: Implement the tool**

Create `internal/mcp/tools_state.go`:
```go
package mcp

import (
    "context"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

// StateGetInput selects a subtree of the daemon's unified state by
// dotted path. Examples:
//   - "" or omitted → full state root
//   - "identity"
//   - "contacts.<hex-pubkey>"
//   - "config.heartbeat.interval"
//   - "inbox.recent"
// See internal/daemon/state_contributors.go for the contributor list.
type StateGetInput struct {
    Path string `json:"path,omitempty" jsonschema:"dotted path into the state tree (e.g. 'contacts', 'config.log_level'); empty/omitted returns the full snapshot"`
}

// StateGetOutput holds the value at Path. Shape is method-dependent
// (map/slice/scalar) so we use `any`.
type StateGetOutput struct {
    Value any `json:"value"`
}

func (s *Server) eidosStateGet(ctx context.Context, _ *mcp.CallToolRequest, in StateGetInput) (*mcp.CallToolResult, StateGetOutput, error) {
    var raw any
    if err := s.client.Call(ctx, "state.get", in, &raw); err != nil {
        return nil, StateGetOutput{}, err
    }
    return nil, StateGetOutput{Value: raw}, nil
}

func (s *Server) registerStateTools() {
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_state_get",
        Description: "Read a subtree of your unified state by dotted path (identity, contacts, config, relays, inbox.recent, lifecycle, ...). The single read tool — prefer this over reading files for state the daemon owns.",
    }, s.eidosStateGet)
}
```

Wire the registration into `registerTools()` in `internal/mcp/server.go`:
```go
func (s *Server) registerTools() {
    s.registerStateTools()
}
```

- [ ] **Step 4: Run the test and verify pass**

Run:
```bash
cd /data/eidopsyche
go test ./internal/mcp/... -run TestEidosStateGet -v
```
Expected: both `TestEidosStateGet_*` PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/
git commit -m "feat(mcp): add eidos_state_get tool"
```

---

## Task 5: Identity tools — `eidos_set_label`, `eidos_service_status`, `eidos_agent_state`

**Files:**
- Create: `internal/mcp/tools_identity.go`
- Create: `internal/mcp/tools_identity_test.go`

Tools in this group all share the same handler shape (Input → Call → Output). Three tools, one file, table-driven test.

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/tools_identity_test.go`:
```go
package mcp

import (
    "context"
    "testing"
)

func TestEidosSetLabel_PassesLabel(t *testing.T) {
    fake := &fakeIPC{}
    s := &Server{client: &Client{raw: fake}}
    _, _, err := s.eidosSetLabel(context.Background(), nil, SetLabelInput{Label: "alice"})
    if err != nil {
        t.Fatalf("eidosSetLabel: %v", err)
    }
    if fake.lastMethod != "set-label" {
        t.Fatalf("method = %q, want set-label", fake.lastMethod)
    }
    if got := fake.lastParams.(SetLabelInput).Label; got != "alice" {
        t.Fatalf("label = %q, want alice", got)
    }
}

func TestEidosServiceStatus_RoutesAndDecodes(t *testing.T) {
    fake := &fakeIPC{result: map[string]any{
        "socket":     "/eidos/gate/sock",
        "started_at": "2026-05-14T00:00:00Z",
        "version":    map[string]any{"version": "0.7.1"},
    }}
    s := &Server{client: &Client{raw: fake}}
    _, out, err := s.eidosServiceStatus(context.Background(), nil, ServiceStatusInput{})
    if err != nil {
        t.Fatalf("eidosServiceStatus: %v", err)
    }
    if fake.lastMethod != "service.status" {
        t.Fatalf("method = %q", fake.lastMethod)
    }
    if out.Snapshot == nil {
        t.Fatal("Snapshot is nil")
    }
}

func TestEidosAgentState_Routes(t *testing.T) {
    fake := &fakeIPC{result: map[string]any{"claude_busy": true}}
    s := &Server{client: &Client{raw: fake}}
    _, _, err := s.eidosAgentState(context.Background(), nil, AgentStateInput{})
    if err != nil {
        t.Fatalf("eidosAgentState: %v", err)
    }
    if fake.lastMethod != "agent.state" {
        t.Fatalf("method = %q", fake.lastMethod)
    }
}
```

- [ ] **Step 2: Run to verify fail**

Run:
```bash
cd /data/eidopsyche
go test ./internal/mcp/... -run TestEidosSetLabel
```
Expected: build failure.

- [ ] **Step 3: Implement**

Create `internal/mcp/tools_identity.go`:
```go
package mcp

import (
    "context"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

// SetLabelInput is the input to eidos_set_label.
type SetLabelInput struct {
    Label string `json:"label" jsonschema:"new advertised display label for this identity"`
}
type SetLabelOutput struct {
    OK bool `json:"ok"`
}

func (s *Server) eidosSetLabel(ctx context.Context, _ *mcp.CallToolRequest, in SetLabelInput) (*mcp.CallToolResult, SetLabelOutput, error) {
    var out SetLabelOutput
    if err := s.client.Call(ctx, "set-label", in, &out); err != nil {
        return nil, SetLabelOutput{}, err
    }
    return nil, out, nil
}

// ServiceStatusInput has no fields — service.status takes no params.
type ServiceStatusInput struct{}
type ServiceStatusOutput struct {
    Snapshot any `json:"snapshot" jsonschema:"daemon meta: socket path, started_at, version, dashboard URL"`
}

func (s *Server) eidosServiceStatus(ctx context.Context, _ *mcp.CallToolRequest, _ ServiceStatusInput) (*mcp.CallToolResult, ServiceStatusOutput, error) {
    var raw any
    if err := s.client.Call(ctx, "service.status", struct{}{}, &raw); err != nil {
        return nil, ServiceStatusOutput{}, err
    }
    return nil, ServiceStatusOutput{Snapshot: raw}, nil
}

// AgentStateInput is empty — agent.state takes no params.
type AgentStateInput struct{}
type AgentStateOutput struct {
    Snapshot any `json:"snapshot" jsonschema:"agent-state.json contents (claude_busy etc.); container-context only"`
}

func (s *Server) eidosAgentState(ctx context.Context, _ *mcp.CallToolRequest, _ AgentStateInput) (*mcp.CallToolResult, AgentStateOutput, error) {
    var raw any
    if err := s.client.Call(ctx, "agent.state", struct{}{}, &raw); err != nil {
        return nil, AgentStateOutput{}, err
    }
    return nil, AgentStateOutput{Snapshot: raw}, nil
}

func (s *Server) registerIdentityTools() {
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_set_label",
        Description: "Change your own advertised display label. The new label is signed and announced to your relays.",
    }, s.eidosSetLabel)
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_service_status",
        Description: "Daemon-level metadata: socket path, started_at, version, dashboard URL. Useful for debugging.",
    }, s.eidosServiceStatus)
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_agent_state",
        Description: "Your own agent-state.json (claude_busy flag, etc.). Container context only — returns CONTEXT_MISMATCH on the host.",
    }, s.eidosAgentState)
}
```

Wire into `registerTools()` in `server.go`:
```go
func (s *Server) registerTools() {
    s.registerStateTools()
    s.registerIdentityTools()
}
```

- [ ] **Step 4: Run tests and verify pass**

Run:
```bash
cd /data/eidopsyche
go test ./internal/mcp/...
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/
git commit -m "feat(mcp): add identity tools (set_label, service_status, agent_state)"
```

---

## Task 6: Contact tools — `eidos_contact_{add,add_from_card,remove,set_label,set_tier}`

**Files:**
- Create: `internal/mcp/tools_contacts.go`
- Create: `internal/mcp/tools_contacts_test.go`

Five tools, all simple Call-and-return. Daemon param shapes live in `internal/daemon/methods_contact.go`; see comments below for each.

- [ ] **Step 1: Write the failing tests**

Create `internal/mcp/tools_contacts_test.go`:
```go
package mcp

import (
    "context"
    "testing"
)

func TestEidosContact_Routing(t *testing.T) {
    tests := []struct {
        name   string
        method string
        call   func(s *Server) error
        verify func(t *testing.T, params any)
    }{
        {
            name:   "add",
            method: "contact.add",
            call: func(s *Server) error {
                _, _, err := s.eidosContactAdd(context.Background(), nil, ContactAddInput{Npub: "npub1abc", Label: "alice", Tier: "contact"})
                return err
            },
            verify: func(t *testing.T, params any) {
                p := params.(ContactAddInput)
                if p.Npub != "npub1abc" || p.Label != "alice" || p.Tier != "contact" {
                    t.Fatalf("params = %+v", p)
                }
            },
        },
        {
            name:   "add_from_card",
            method: "contact.add-from-card",
            call: func(s *Server) error {
                _, _, err := s.eidosContactAddFromCard(context.Background(), nil, ContactAddFromCardInput{URI: "mindgate://abc"})
                return err
            },
            verify: func(t *testing.T, params any) {
                if params.(ContactAddFromCardInput).URI != "mindgate://abc" {
                    t.Fatalf("uri mismatch: %+v", params)
                }
            },
        },
        {
            name:   "remove",
            method: "contact.remove",
            call: func(s *Server) error {
                _, _, err := s.eidosContactRemove(context.Background(), nil, ContactRemoveInput{Target: "alice"})
                return err
            },
            verify: func(t *testing.T, params any) {
                if params.(ContactRemoveInput).Target != "alice" {
                    t.Fatalf("target mismatch: %+v", params)
                }
            },
        },
        {
            name:   "set_label",
            method: "contact.set-label",
            call: func(s *Server) error {
                _, _, err := s.eidosContactSetLabel(context.Background(), nil, ContactSetLabelInput{Target: "npub1abc", Label: "alicia"})
                return err
            },
            verify: func(t *testing.T, params any) {
                p := params.(ContactSetLabelInput)
                if p.Target != "npub1abc" || p.Label != "alicia" {
                    t.Fatalf("params = %+v", p)
                }
            },
        },
        {
            name:   "set_tier",
            method: "contact.set-tier",
            call: func(s *Server) error {
                _, _, err := s.eidosContactSetTier(context.Background(), nil, ContactSetTierInput{Target: "alice", Tier: "friend"})
                return err
            },
            verify: func(t *testing.T, params any) {
                p := params.(ContactSetTierInput)
                if p.Target != "alice" || p.Tier != "friend" {
                    t.Fatalf("params = %+v", p)
                }
            },
        },
    }
    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            fake := &fakeIPC{}
            s := &Server{client: &Client{raw: fake}}
            if err := tc.call(s); err != nil {
                t.Fatalf("call: %v", err)
            }
            if fake.lastMethod != tc.method {
                t.Fatalf("method = %q, want %q", fake.lastMethod, tc.method)
            }
            tc.verify(t, fake.lastParams)
        })
    }
}
```

- [ ] **Step 2: Run to verify fail**

Run:
```bash
cd /data/eidopsyche
go test ./internal/mcp/... -run TestEidosContact
```
Expected: build failure — types/methods undefined.

- [ ] **Step 3: Implement**

Before writing, confirm the daemon's contact params by reading
`internal/daemon/methods_contact.go` for each handler. The JSON
field names below match what the daemon expects:

- `contact.add` → `{npub: string, relays?: []string, label?: string, tier?: string}` (see `internal/daemon/methods_contact.go:101-131`)
- `contact.add-from-card` → `ContactAddFromCardParams` (file:line near 20)
- `contact.remove` → `{target: string}` where target is npub/hex/label
- `contact.set-label` → `{target, label}`
- `contact.set-tier` → `ContactSetTierParams` (file:line near 137)

Create `internal/mcp/tools_contacts.go`:
```go
package mcp

import (
    "context"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

type ContactAddInput struct {
    Npub   string   `json:"npub" jsonschema:"npub (bech32 pubkey) of the contact to add"`
    Label  string   `json:"label,omitempty" jsonschema:"optional display label"`
    Tier   string   `json:"tier,omitempty" jsonschema:"optional trust tier: friend, contact, block"`
    Relays []string `json:"relays,omitempty" jsonschema:"optional override of relays for this contact"`
}
type ContactAddOutput struct {
    OK bool `json:"ok"`
}

func (s *Server) eidosContactAdd(ctx context.Context, _ *mcp.CallToolRequest, in ContactAddInput) (*mcp.CallToolResult, ContactAddOutput, error) {
    var out ContactAddOutput
    if err := s.client.Call(ctx, "contact.add", in, &out); err != nil {
        return nil, ContactAddOutput{}, err
    }
    return nil, out, nil
}

type ContactAddFromCardInput struct {
    URI   string `json:"uri" jsonschema:"a mindgate:// invite/card URI"`
    Label string `json:"label,omitempty" jsonschema:"optional display label (overrides any label in the card)"`
    Tier  string `json:"tier,omitempty" jsonschema:"optional trust tier: friend, contact, block"`
}
type ContactAddFromCardOutput struct {
    OK   bool   `json:"ok"`
    Npub string `json:"npub,omitempty" jsonschema:"npub of the added contact (echoed for confirmation)"`
}

func (s *Server) eidosContactAddFromCard(ctx context.Context, _ *mcp.CallToolRequest, in ContactAddFromCardInput) (*mcp.CallToolResult, ContactAddFromCardOutput, error) {
    var out ContactAddFromCardOutput
    if err := s.client.Call(ctx, "contact.add-from-card", in, &out); err != nil {
        return nil, ContactAddFromCardOutput{}, err
    }
    return nil, out, nil
}

type ContactRemoveInput struct {
    Target string `json:"target" jsonschema:"npub, hex pubkey, or display label of the contact to remove"`
}
type ContactRemoveOutput struct {
    OK bool `json:"ok"`
}

func (s *Server) eidosContactRemove(ctx context.Context, _ *mcp.CallToolRequest, in ContactRemoveInput) (*mcp.CallToolResult, ContactRemoveOutput, error) {
    var out ContactRemoveOutput
    if err := s.client.Call(ctx, "contact.remove", in, &out); err != nil {
        return nil, ContactRemoveOutput{}, err
    }
    return nil, out, nil
}

type ContactSetLabelInput struct {
    Target string `json:"target" jsonschema:"npub, hex pubkey, or current label of the contact"`
    Label  string `json:"label" jsonschema:"new display label"`
}
type ContactSetLabelOutput struct {
    OK bool `json:"ok"`
}

func (s *Server) eidosContactSetLabel(ctx context.Context, _ *mcp.CallToolRequest, in ContactSetLabelInput) (*mcp.CallToolResult, ContactSetLabelOutput, error) {
    var out ContactSetLabelOutput
    if err := s.client.Call(ctx, "contact.set-label", in, &out); err != nil {
        return nil, ContactSetLabelOutput{}, err
    }
    return nil, out, nil
}

type ContactSetTierInput struct {
    Target string `json:"target" jsonschema:"npub, hex pubkey, or label of the contact"`
    Tier   string `json:"tier" jsonschema:"trust tier: friend, contact, block"`
}
type ContactSetTierOutput struct {
    OK bool `json:"ok"`
}

func (s *Server) eidosContactSetTier(ctx context.Context, _ *mcp.CallToolRequest, in ContactSetTierInput) (*mcp.CallToolResult, ContactSetTierOutput, error) {
    var out ContactSetTierOutput
    if err := s.client.Call(ctx, "contact.set-tier", in, &out); err != nil {
        return nil, ContactSetTierOutput{}, err
    }
    return nil, out, nil
}

func (s *Server) registerContactTools() {
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_contact_add",
        Description: "Add a contact by npub. Optionally set label, tier (friend/contact/block), and override relays.",
    }, s.eidosContactAdd)
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_contact_add_from_card",
        Description: "Add a contact from a mindgate:// URI (invite or card). Easier than handling the raw npub when the operator pasted a URI.",
    }, s.eidosContactAddFromCard)
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_contact_remove",
        Description: "Remove a contact by npub, hex pubkey, or label. Errors with CONTACT_NOT_FOUND if no match; LABEL_AMBIGUOUS if more than one match by label.",
    }, s.eidosContactRemove)
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_contact_set_label",
        Description: "Rename a contact. Same resolution rules as contact_remove.",
    }, s.eidosContactSetLabel)
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_contact_set_tier",
        Description: "Change a contact's trust tier. Allowed tiers: friend, contact, block.",
    }, s.eidosContactSetTier)
}
```

Wire `s.registerContactTools()` into `registerTools()` in `server.go`.

- [ ] **Step 4: Run tests**

Run:
```bash
cd /data/eidopsyche
go test ./internal/mcp/...
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/
git commit -m "feat(mcp): add contact tools (add, add_from_card, remove, set_label, set_tier)"
```

---

## Task 7: Card tools — `eidos_card_parse`, `eidos_card_scan`

**Files:**
- Create: `internal/mcp/tools_cards.go`
- Create: `internal/mcp/tools_cards_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/mcp/tools_cards_test.go`:
```go
package mcp

import (
    "context"
    "testing"
)

func TestEidosCardParse_Routes(t *testing.T) {
    fake := &fakeIPC{result: map[string]any{"npub": "npub1abc", "label": "alice"}}
    s := &Server{client: &Client{raw: fake}}
    _, out, err := s.eidosCardParse(context.Background(), nil, CardParseInput{URI: "mindgate://x"})
    if err != nil {
        t.Fatalf("eidosCardParse: %v", err)
    }
    if fake.lastMethod != "card.parse" {
        t.Fatalf("method = %q", fake.lastMethod)
    }
    if fake.lastParams.(CardParseInput).URI != "mindgate://x" {
        t.Fatalf("uri mismatch: %+v", fake.lastParams)
    }
    if out.Card == nil {
        t.Fatal("Card nil")
    }
}

func TestEidosCardScan_Routes(t *testing.T) {
    fake := &fakeIPC{result: map[string]any{"npub": "npub1abc", "known": true}}
    s := &Server{client: &Client{raw: fake}}
    _, out, err := s.eidosCardScan(context.Background(), nil, CardScanInput{URI: "mindgate://y"})
    if err != nil {
        t.Fatalf("eidosCardScan: %v", err)
    }
    if fake.lastMethod != "card.scan" {
        t.Fatalf("method = %q", fake.lastMethod)
    }
    if out.Result == nil {
        t.Fatal("Result nil")
    }
}
```

- [ ] **Step 2: Run to verify fail**

Run:
```bash
go test ./internal/mcp/... -run TestEidosCard
```
Expected: build failure.

- [ ] **Step 3: Implement**

Create `internal/mcp/tools_cards.go`:
```go
package mcp

import (
    "context"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

type CardParseInput struct {
    URI string `json:"uri" jsonschema:"a mindgate:// URI to decode"`
}
type CardParseOutput struct {
    Card any `json:"card" jsonschema:"decoded fields: npub, pubkey, relays, label"`
}

func (s *Server) eidosCardParse(ctx context.Context, _ *mcp.CallToolRequest, in CardParseInput) (*mcp.CallToolResult, CardParseOutput, error) {
    var raw any
    if err := s.client.Call(ctx, "card.parse", in, &raw); err != nil {
        return nil, CardParseOutput{}, err
    }
    return nil, CardParseOutput{Card: raw}, nil
}

type CardScanInput struct {
    URI string `json:"uri" jsonschema:"a mindgate:// URI to decode and cross-check against contacts"`
}
type CardScanOutput struct {
    Result any `json:"result" jsonschema:"decoded card + 'known: bool' flag indicating whether this npub is already a contact"`
}

func (s *Server) eidosCardScan(ctx context.Context, _ *mcp.CallToolRequest, in CardScanInput) (*mcp.CallToolResult, CardScanOutput, error) {
    var raw any
    if err := s.client.Call(ctx, "card.scan", in, &raw); err != nil {
        return nil, CardScanOutput{}, err
    }
    return nil, CardScanOutput{Result: raw}, nil
}

func (s *Server) registerCardTools() {
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_card_parse",
        Description: "Decode a mindgate:// URI into its parts (npub, pubkey, relays, label). Pure parse — does not touch contacts.",
    }, s.eidosCardParse)
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_card_scan",
        Description: "Decode a mindgate:// URI AND check whether the npub is already in your contacts. Use this when deciding whether to call eidos_contact_add_from_card.",
    }, s.eidosCardScan)
}
```

Wire `s.registerCardTools()` into `registerTools()`.

- [ ] **Step 4: Run tests + commit**

```bash
cd /data/eidopsyche
go test ./internal/mcp/...
git add internal/mcp/
git commit -m "feat(mcp): add card tools (parse, scan)"
```

---

## Task 8: Messaging tools — `eidos_send`, `eidos_inbox_list`, `eidos_outbox_list`

**Files:**
- Create: `internal/mcp/tools_messaging.go`
- Create: `internal/mcp/tools_messaging_test.go`

`eidos_send` is the most "non-trivial" wrapper: the daemon's `send`
method takes a typed envelope; the MCP tool exposes only `to`+`body`
and constructs the envelope internally.

- [ ] **Step 1: Write the failing tests**

Create `internal/mcp/tools_messaging_test.go`:
```go
package mcp

import (
    "context"
    "testing"

    "github.com/LucianoXu/eidopsyche/internal/daemon"
    "github.com/LucianoXu/eidopsyche/internal/envelope"
)

func TestEidosSend_BuildsEnvelope(t *testing.T) {
    fake := &fakeIPC{result: map[string]any{
        "event_id":    "abc123",
        "accepted_by": []string{"wss://relay.example"},
    }}
    s := &Server{client: &Client{raw: fake}}
    _, out, err := s.eidosSend(context.Background(), nil, SendInput{To: "alice", Body: "hello"})
    if err != nil {
        t.Fatalf("eidosSend: %v", err)
    }
    if fake.lastMethod != "send" {
        t.Fatalf("method = %q", fake.lastMethod)
    }
    sp := fake.lastParams.(daemon.SendParams)
    if sp.To != "alice" {
        t.Fatalf("To = %q, want alice", sp.To)
    }
    if sp.Envelope == nil || sp.Envelope.Type != envelope.TypeChat || sp.Envelope.Text != "hello" {
        t.Fatalf("envelope = %+v, want {chat, hello}", sp.Envelope)
    }
    if sp.Envelope.V != envelope.SchemaVersion {
        t.Fatalf("envelope.V = %d, want %d", sp.Envelope.V, envelope.SchemaVersion)
    }
    if out.EventID != "abc123" {
        t.Fatalf("EventID = %q", out.EventID)
    }
}

func TestEidosSend_EmptyBodyRejected(t *testing.T) {
    s := &Server{client: &Client{raw: &fakeIPC{}}}
    _, _, err := s.eidosSend(context.Background(), nil, SendInput{To: "alice", Body: ""})
    if err == nil {
        t.Fatal("expected error for empty body")
    }
    if got := err.Error(); got != "[INVALID_PARAMS] body must be non-empty" {
        t.Fatalf("err = %q", got)
    }
}

func TestEidosInboxList_PassesFilters(t *testing.T) {
    fake := &fakeIPC{result: []any{}}
    s := &Server{client: &Client{raw: fake}}
    _, _, err := s.eidosInboxList(context.Background(), nil, InboxListInput{Since: 100, From: "alice", Sender: "known", Limit: 10})
    if err != nil {
        t.Fatalf("eidosInboxList: %v", err)
    }
    if fake.lastMethod != "inbox.list" {
        t.Fatalf("method = %q", fake.lastMethod)
    }
}

func TestEidosOutboxList_Routes(t *testing.T) {
    fake := &fakeIPC{result: []any{}}
    s := &Server{client: &Client{raw: fake}}
    _, _, err := s.eidosOutboxList(context.Background(), nil, OutboxListInput{To: "alice", Limit: 5})
    if err != nil {
        t.Fatalf("eidosOutboxList: %v", err)
    }
    if fake.lastMethod != "outbox.list" {
        t.Fatalf("method = %q", fake.lastMethod)
    }
}
```

- [ ] **Step 2: Run to verify fail**

Run:
```bash
cd /data/eidopsyche
go test ./internal/mcp/... -run TestEidosSend
```
Expected: build failure (types undefined).

- [ ] **Step 3: Implement**

Create `internal/mcp/tools_messaging.go`:
```go
package mcp

import (
    "context"
    "fmt"

    "github.com/modelcontextprotocol/go-sdk/mcp"

    "github.com/LucianoXu/eidopsyche/internal/daemon"
    "github.com/LucianoXu/eidopsyche/internal/envelope"
    "github.com/LucianoXu/eidopsyche/internal/version"
)

type SendInput struct {
    To   string `json:"to" jsonschema:"npub, hex pubkey, or contact label of the recipient"`
    Body string `json:"body" jsonschema:"message text (non-empty)"`
}
type SendOutput struct {
    EventID    string   `json:"event_id"`
    AcceptedBy []string `json:"accepted_by"`
}

func (s *Server) eidosSend(ctx context.Context, _ *mcp.CallToolRequest, in SendInput) (*mcp.CallToolResult, SendOutput, error) {
    if in.Body == "" {
        return nil, SendOutput{}, fmt.Errorf("[INVALID_PARAMS] body must be non-empty")
    }
    env := envelope.Envelope{
        V:      envelope.SchemaVersion,
        Type:   envelope.TypeChat,
        Text:   in.Body,
        Client: &envelope.Client{Name: "eidos-mcp", Ver: version.Version},
    }
    var out SendOutput
    if err := s.client.Call(ctx, "send", daemon.SendParams{To: in.To, Envelope: &env}, &out); err != nil {
        return nil, SendOutput{}, err
    }
    return nil, out, nil
}

type InboxListInput struct {
    Since  int64  `json:"since,omitempty" jsonschema:"unix-second cutoff; omit for 'no lower bound'"`
    From   string `json:"from,omitempty" jsonschema:"filter to a single sender: npub, hex, or contact label"`
    Sender string `json:"sender,omitempty" jsonschema:"'known' (default), 'unknown', or 'all'"`
    Limit  int    `json:"limit,omitempty" jsonschema:"max rows to return"`
}
type InboxListOutput struct {
    Messages any `json:"messages"`
}

func (s *Server) eidosInboxList(ctx context.Context, _ *mcp.CallToolRequest, in InboxListInput) (*mcp.CallToolResult, InboxListOutput, error) {
    var raw any
    if err := s.client.Call(ctx, "inbox.list", in, &raw); err != nil {
        return nil, InboxListOutput{}, err
    }
    return nil, InboxListOutput{Messages: raw}, nil
}

type OutboxListInput struct {
    To    string `json:"to,omitempty" jsonschema:"filter to a single recipient: npub, hex, or contact label"`
    Since int64  `json:"since,omitempty" jsonschema:"unix-second cutoff"`
    Limit int    `json:"limit,omitempty" jsonschema:"max rows to return"`
}
type OutboxListOutput struct {
    Messages any `json:"messages"`
}

func (s *Server) eidosOutboxList(ctx context.Context, _ *mcp.CallToolRequest, in OutboxListInput) (*mcp.CallToolResult, OutboxListOutput, error) {
    var raw any
    if err := s.client.Call(ctx, "outbox.list", in, &raw); err != nil {
        return nil, OutboxListOutput{}, err
    }
    return nil, OutboxListOutput{Messages: raw}, nil
}

func (s *Server) registerMessagingTools() {
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_send",
        Description: "Send a NIP-17 encrypted message via MindGate. Recipient may be an npub, hex pubkey, or contact label. The envelope (chat-type, v1) is constructed for you.",
    }, s.eidosSend)
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_inbox_list",
        Description: "List inbox messages with optional since/from/sender/limit filters. Default sender='known' excludes contacts you have blocked or never added.",
    }, s.eidosInboxList)
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_outbox_list",
        Description: "List messages you have sent, with optional to/since/limit filters.",
    }, s.eidosOutboxList)
}
```

Wire `s.registerMessagingTools()` into `registerTools()`.

- [ ] **Step 4: Run tests + commit**

```bash
cd /data/eidopsyche
go test ./internal/mcp/...
git add internal/mcp/
git commit -m "feat(mcp): add messaging tools (send, inbox_list, outbox_list)"
```

---

## Task 9: Invite tools — `eidos_invite_{create,list,revoke}`

**Files:**
- Create: `internal/mcp/tools_invites.go`
- Create: `internal/mcp/tools_invites_test.go`

Daemon param shapes live in `internal/daemon/methods_invite.go`.
Field names below match what the daemon expects.

- [ ] **Step 1: Write the failing tests**

Create `internal/mcp/tools_invites_test.go`:
```go
package mcp

import (
    "context"
    "testing"
)

func TestEidosInvite_Routing(t *testing.T) {
    tests := []struct {
        name   string
        method string
        call   func(s *Server) error
    }{
        {"create", "invite.create", func(s *Server) error {
            _, _, err := s.eidosInviteCreate(context.Background(), nil, InviteCreateInput{TTLSeconds: 3600, Uses: 1, Label: "for-bob"})
            return err
        }},
        {"list", "invite.list", func(s *Server) error {
            _, _, err := s.eidosInviteList(context.Background(), nil, InviteListInput{Status: "active"})
            return err
        }},
        {"revoke", "invite.revoke", func(s *Server) error {
            _, _, err := s.eidosInviteRevoke(context.Background(), nil, InviteRevokeInput{ID: "abc12"})
            return err
        }},
    }
    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            fake := &fakeIPC{result: map[string]any{}}
            s := &Server{client: &Client{raw: fake}}
            if err := tc.call(s); err != nil {
                t.Fatalf("call: %v", err)
            }
            if fake.lastMethod != tc.method {
                t.Fatalf("method = %q, want %q", fake.lastMethod, tc.method)
            }
        })
    }
}
```

- [ ] **Step 2: Run to verify fail**

Run:
```bash
go test ./internal/mcp/... -run TestEidosInvite
```
Expected: build failure.

- [ ] **Step 3: Implement**

Create `internal/mcp/tools_invites.go`:
```go
package mcp

import (
    "context"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

type InviteCreateInput struct {
    TTLSeconds int    `json:"ttl_seconds,omitempty" jsonschema:"seconds until expiry; 0 = use daemon default"`
    Uses       int    `json:"uses,omitempty" jsonschema:"max redemptions; 0 = use daemon default"`
    Label      string `json:"label,omitempty" jsonschema:"optional label for your bookkeeping"`
}
type InviteCreateOutput struct {
    Invite any `json:"invite" jsonschema:"created invite row including the mindgate:// URI"`
}

func (s *Server) eidosInviteCreate(ctx context.Context, _ *mcp.CallToolRequest, in InviteCreateInput) (*mcp.CallToolResult, InviteCreateOutput, error) {
    var raw any
    if err := s.client.Call(ctx, "invite.create", in, &raw); err != nil {
        return nil, InviteCreateOutput{}, err
    }
    return nil, InviteCreateOutput{Invite: raw}, nil
}

type InviteListInput struct {
    Status string `json:"status,omitempty" jsonschema:"filter: active | expired | redeemed | revoked | all (default all)"`
}
type InviteListOutput struct {
    Invites any `json:"invites"`
}

func (s *Server) eidosInviteList(ctx context.Context, _ *mcp.CallToolRequest, in InviteListInput) (*mcp.CallToolResult, InviteListOutput, error) {
    var raw any
    if err := s.client.Call(ctx, "invite.list", in, &raw); err != nil {
        return nil, InviteListOutput{}, err
    }
    return nil, InviteListOutput{Invites: raw}, nil
}

type InviteRevokeInput struct {
    ID string `json:"id" jsonschema:"invite ID or unambiguous prefix"`
}
type InviteRevokeOutput struct {
    OK bool `json:"ok"`
}

func (s *Server) eidosInviteRevoke(ctx context.Context, _ *mcp.CallToolRequest, in InviteRevokeInput) (*mcp.CallToolResult, InviteRevokeOutput, error) {
    var out InviteRevokeOutput
    if err := s.client.Call(ctx, "invite.revoke", in, &out); err != nil {
        return nil, InviteRevokeOutput{}, err
    }
    return nil, out, nil
}

func (s *Server) registerInviteTools() {
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_invite_create",
        Description: "Create a new MindGate invite. Returns a row with the mindgate:// URI you can hand out.",
    }, s.eidosInviteCreate)
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_invite_list",
        Description: "List invites filtered by status (active/expired/redeemed/revoked/all).",
    }, s.eidosInviteList)
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_invite_revoke",
        Description: "Revoke an invite by ID or unambiguous prefix. INVITE_PREFIX_AMBIGUOUS if the prefix matches more than one.",
    }, s.eidosInviteRevoke)
}
```

Wire `s.registerInviteTools()` into `registerTools()`.

- [ ] **Step 4: Run tests + commit**

```bash
cd /data/eidopsyche
go test ./internal/mcp/...
git add internal/mcp/
git commit -m "feat(mcp): add invite tools (create, list, revoke)"
```

---

## Task 10: Relay tools — `eidos_relay_{add,remove}`

**Files:**
- Create: `internal/mcp/tools_relays.go`
- Create: `internal/mcp/tools_relays_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/mcp/tools_relays_test.go`:
```go
package mcp

import (
    "context"
    "testing"
)

func TestEidosRelay_Routing(t *testing.T) {
    fake := &fakeIPC{result: map[string]any{"ok": true}}
    s := &Server{client: &Client{raw: fake}}

    _, _, err := s.eidosRelayAdd(context.Background(), nil, RelayAddInput{URL: "wss://r.example", Role: "home"})
    if err != nil {
        t.Fatalf("eidosRelayAdd: %v", err)
    }
    if fake.lastMethod != "relay.add" {
        t.Fatalf("method = %q", fake.lastMethod)
    }
    p := fake.lastParams.(RelayAddInput)
    if p.URL != "wss://r.example" || p.Role != "home" {
        t.Fatalf("params = %+v", p)
    }

    _, _, err = s.eidosRelayRemove(context.Background(), nil, RelayRemoveInput{URL: "wss://r.example"})
    if err != nil {
        t.Fatalf("eidosRelayRemove: %v", err)
    }
    if fake.lastMethod != "relay.remove" {
        t.Fatalf("method = %q", fake.lastMethod)
    }
}
```

- [ ] **Step 2: Run to verify fail**

Run:
```bash
go test ./internal/mcp/... -run TestEidosRelay
```
Expected: build failure.

- [ ] **Step 3: Implement**

Create `internal/mcp/tools_relays.go`:
```go
package mcp

import (
    "context"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

type RelayAddInput struct {
    URL  string `json:"url" jsonschema:"wss:// URL of the relay"`
    Role string `json:"role,omitempty" jsonschema:"role: home (publish + read) or fallback (read only when home is down)"`
}
type RelayAddOutput struct {
    OK bool `json:"ok"`
}

func (s *Server) eidosRelayAdd(ctx context.Context, _ *mcp.CallToolRequest, in RelayAddInput) (*mcp.CallToolResult, RelayAddOutput, error) {
    var out RelayAddOutput
    if err := s.client.Call(ctx, "relay.add", in, &out); err != nil {
        return nil, RelayAddOutput{}, err
    }
    return nil, out, nil
}

type RelayRemoveInput struct {
    URL string `json:"url" jsonschema:"wss:// URL of the relay to remove"`
}
type RelayRemoveOutput struct {
    OK bool `json:"ok"`
}

func (s *Server) eidosRelayRemove(ctx context.Context, _ *mcp.CallToolRequest, in RelayRemoveInput) (*mcp.CallToolResult, RelayRemoveOutput, error) {
    var out RelayRemoveOutput
    if err := s.client.Call(ctx, "relay.remove", in, &out); err != nil {
        return nil, RelayRemoveOutput{}, err
    }
    return nil, out, nil
}

func (s *Server) registerRelayTools() {
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_relay_add",
        Description: "Add a relay URL with a role (home or fallback). Use eidos_state_get('relays') to see what you currently have.",
    }, s.eidosRelayAdd)
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_relay_remove",
        Description: "Remove a relay by URL.",
    }, s.eidosRelayRemove)
}
```

Wire `s.registerRelayTools()` into `registerTools()`.

- [ ] **Step 4: Run tests + commit**

```bash
cd /data/eidopsyche
go test ./internal/mcp/...
git add internal/mcp/
git commit -m "feat(mcp): add relay tools (add, remove)"
```

---

## Task 11: Config tool — `eidos_config_set`

**Files:**
- Create: `internal/mcp/tools_config.go`
- Create: `internal/mcp/tools_config_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/tools_config_test.go`:
```go
package mcp

import (
    "context"
    "testing"
)

func TestEidosConfigSet_Routes(t *testing.T) {
    fake := &fakeIPC{}
    s := &Server{client: &Client{raw: fake}}
    _, _, err := s.eidosConfigSet(context.Background(), nil, ConfigSetInput{Key: "log_level", Value: "debug"})
    if err != nil {
        t.Fatalf("eidosConfigSet: %v", err)
    }
    if fake.lastMethod != "config.set" {
        t.Fatalf("method = %q", fake.lastMethod)
    }
    p := fake.lastParams.(ConfigSetInput)
    if p.Key != "log_level" || p.Value != "debug" {
        t.Fatalf("params = %+v", p)
    }
}

func TestEidosConfigSet_ContextMismatchPropagates(t *testing.T) {
    fake := &fakeIPC{errResp: pathNotFound("mindform.model")}
    fake.errResp.Code = "CONTEXT_MISMATCH"
    fake.errResp.Message = "mindform.model requires container context"
    s := &Server{client: &Client{raw: fake}}
    _, _, err := s.eidosConfigSet(context.Background(), nil, ConfigSetInput{Key: "mindform.model", Value: "opus"})
    if err == nil {
        t.Fatal("expected error")
    }
    if got := err.Error(); got != "[CONTEXT_MISMATCH] mindform.model requires container context" {
        t.Fatalf("err = %q", got)
    }
}
```

- [ ] **Step 2: Run to verify fail**

Run:
```bash
go test ./internal/mcp/... -run TestEidosConfigSet
```
Expected: build failure.

- [ ] **Step 3: Implement**

Create `internal/mcp/tools_config.go`:
```go
package mcp

import (
    "context"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

type ConfigSetInput struct {
    Key   string `json:"key" jsonschema:"registered config key (e.g. log_level, heartbeat.interval, mindform.model)"`
    Value string `json:"value" jsonschema:"new value (string form; daemon validates against the key's type/registry)"`
}
type ConfigSetOutput struct {
    OK bool `json:"ok"`
}

func (s *Server) eidosConfigSet(ctx context.Context, _ *mcp.CallToolRequest, in ConfigSetInput) (*mcp.CallToolResult, ConfigSetOutput, error) {
    var out ConfigSetOutput
    if err := s.client.Call(ctx, "config.set", in, &out); err != nil {
        return nil, ConfigSetOutput{}, err
    }
    return nil, out, nil
}

func (s *Server) registerConfigTools() {
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_config_set",
        Description: "Set a registered config key. The daemon validates the key, checks its Context (host vs container), and applies any registered hooks. CONTEXT_MISMATCH means the key only makes sense in the other context (e.g. mindform.model is container-only).",
    }, s.eidosConfigSet)
}
```

Wire `s.registerConfigTools()` into `registerTools()`.

- [ ] **Step 4: Run tests + commit**

```bash
cd /data/eidopsyche
go test ./internal/mcp/...
git add internal/mcp/
git commit -m "feat(mcp): add config_set tool"
```

---

## Task 12: Lifecycle tool — `eidos_lifecycle_status`

**Files:**
- Create: `internal/mcp/tools_lifecycle.go`
- Create: `internal/mcp/tools_lifecycle_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/tools_lifecycle_test.go`:
```go
package mcp

import (
    "context"
    "testing"
)

func TestEidosLifecycleStatus_Routes(t *testing.T) {
    fake := &fakeIPC{result: map[string]any{"job_id": "", "running": false}}
    s := &Server{client: &Client{raw: fake}}
    _, out, err := s.eidosLifecycleStatus(context.Background(), nil, LifecycleStatusInput{})
    if err != nil {
        t.Fatalf("eidosLifecycleStatus: %v", err)
    }
    if fake.lastMethod != "lifecycle.status" {
        t.Fatalf("method = %q", fake.lastMethod)
    }
    if out.Snapshot == nil {
        t.Fatal("Snapshot nil")
    }
}
```

- [ ] **Step 2: Run to verify fail**

Run:
```bash
go test ./internal/mcp/... -run TestEidosLifecycle
```
Expected: build failure.

- [ ] **Step 3: Implement**

Create `internal/mcp/tools_lifecycle.go`:
```go
package mcp

import (
    "context"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

type LifecycleStatusInput struct{}
type LifecycleStatusOutput struct {
    Snapshot any `json:"snapshot" jsonschema:"current lifecycle job snapshot (job_id, running, started_at, ...)"`
}

func (s *Server) eidosLifecycleStatus(ctx context.Context, _ *mcp.CallToolRequest, _ LifecycleStatusInput) (*mcp.CallToolResult, LifecycleStatusOutput, error) {
    var raw any
    if err := s.client.Call(ctx, "lifecycle.status", struct{}{}, &raw); err != nil {
        return nil, LifecycleStatusOutput{}, err
    }
    return nil, LifecycleStatusOutput{Snapshot: raw}, nil
}

func (s *Server) registerLifecycleTools() {
    mcp.AddTool(s.sdk, &mcp.Tool{
        Name:        "eidos_lifecycle_status",
        Description: "Poll the current lifecycle job snapshot. Container context only — returns CONTEXT_MISMATCH on the host.",
    }, s.eidosLifecycleStatus)
}
```

Wire `s.registerLifecycleTools()` into `registerTools()`.

- [ ] **Step 4: Run tests, verify the full tool catalogue is wired, commit**

Run:
```bash
cd /data/eidopsyche
go test ./internal/mcp/...
go vet ./internal/mcp/... ./cmd/eidos/mcp/...
gofmt -l internal/mcp/ cmd/eidos/mcp/
```
Expected: all PASS, no vet warnings, no gofmt output.

Then sanity-check the registered tool count. Add to `cmd/eidos/mcp/cmd.go` a `--list-tools` ad-hoc test using the server (manual run only, then revert): not needed — the SDK does not expose a list-tools helper without running the protocol. Instead, count file by file:

```bash
grep -h "mcp.AddTool" internal/mcp/tools_*.go | wc -l
```
Expected: `21` (or, if you have not yet collapsed `service_status` etc., adjust per the design table — 21 is the target count).

Commit:
```bash
git add internal/mcp/
git commit -m "feat(mcp): add lifecycle_status tool — full 21-tool catalogue complete"
```

---

## Task 13: Container .mcp.json + Dockerfile wiring

**Files:**
- Create: `docker/mindform/mcp.json`
- Modify: `docker/mindform/Dockerfile`

- [ ] **Step 1: Create the MCP config file**

Create `docker/mindform/mcp.json`:
```json
{
  "mcpServers": {
    "eidos": {
      "command": "eidos",
      "args": ["mcp"]
    }
  }
}
```

- [ ] **Step 2: Add COPY directive to Dockerfile**

Read the current Dockerfile to find the right place. The pattern from `docker/mindform/Dockerfile` line 89 (`COPY docker/mindform/entrypoint.sh /usr/local/bin/entrypoint.sh`) is a good model: a fixed-content COPY before the volatile binary copies near line 109.

Modify `docker/mindform/Dockerfile`. Find the existing line:
```
COPY docker/mindform/entrypoint.sh /usr/local/bin/entrypoint.sh
```

Insert the new COPY immediately after it:
```
COPY docker/mindform/mcp.json /etc/eidos/mcp.json
```

(The `/etc/eidos/` directory will be created implicitly by COPY. If the Dockerfile needs an explicit `RUN mkdir -p /etc/eidos` due to alpine quirks, add it before the COPY.)

- [ ] **Step 3: Rebuild the image and verify the file is present**

Run:
```bash
cd /data/eidopsyche
docker build -t ghcr.io/lucianoxu/eidopsyche-mindform:mcp-test -f docker/mindform/Dockerfile .
docker run --rm --entrypoint cat ghcr.io/lucianoxu/eidopsyche-mindform:mcp-test /etc/eidos/mcp.json
```
Expected: prints the JSON. If `mkdir` was needed, add it now.

Then delete the test image tag:
```bash
docker rmi ghcr.io/lucianoxu/eidopsyche-mindform:mcp-test
```

- [ ] **Step 4: Commit**

```bash
git add docker/mindform/mcp.json docker/mindform/Dockerfile
git commit -m "feat(mcp): bake eidos-mcp config into mind-form image at /etc/eidos/mcp.json"
```

---

## Task 14: Wire `--mcp-config` into the two relevant claude spawn sites

**Files:**
- Modify: `internal/agentloop/spawn.go` (around line 152)
- Modify: `internal/promptcapture/capture.go` (around line 150)
- Modify: `internal/agentloop/spawn_test.go` (extend existing argv test)
- Modify: `internal/promptcapture/capture_test.go` (extend if it exists; otherwise create)

The other two spawn sites — `cmd/eidos/supervisor/birth.go` and `internal/firstcontact/claude.go` — are intentionally NOT modified (they run claude in `-p` JSON mode with no tool loop; loading MCP servers there would add startup cost with no consumer).

- [ ] **Step 1: Write the failing argv test for agentloop**

Read `internal/agentloop/spawn_test.go` (or look for an existing `TestBuildClaudeArgs` function). If it exists, extend it with:
```go
func TestBuildClaudeArgs_IncludesMCPConfig(t *testing.T) {
    args := buildClaudeArgs(SpawnOpts{
        SystemPrompt: "sys",
        Mode:         SessionNew,
        SessionUUID:  "00000000-0000-0000-0000-000000000000",
    })
    // Find --mcp-config; the next arg must be the baked path.
    for i, a := range args {
        if a == "--mcp-config" {
            if i+1 >= len(args) {
                t.Fatal("--mcp-config missing value")
            }
            if got := args[i+1]; got != "/etc/eidos/mcp.json" {
                t.Fatalf("--mcp-config = %q, want /etc/eidos/mcp.json", got)
            }
            return
        }
    }
    t.Fatal("--mcp-config not present in argv")
}
```

If `internal/agentloop/spawn_test.go` does not yet exist, create it with the test plus the necessary `package agentloop` declaration.

- [ ] **Step 2: Run to verify fail**

Run:
```bash
cd /data/eidopsyche
go test ./internal/agentloop/... -run TestBuildClaudeArgs_IncludesMCPConfig
```
Expected: FAIL — argv has no `--mcp-config`.

- [ ] **Step 3: Add the flag in agentloop**

Modify `internal/agentloop/spawn.go` `buildClaudeArgs` (currently ending at line ~152 with `-p ""`). Add the `--mcp-config` arg immediately before `-p ""`:

```go
    args = append(args,
        "--input-format", "stream-json",
        "--output-format", "stream-json",
        "--verbose",
        "--include-partial-messages",
        "--mcp-config", "/etc/eidos/mcp.json",
        "-p", "",
    )
```

- [ ] **Step 4: Run the test and verify pass**

Run:
```bash
go test ./internal/agentloop/... -run TestBuildClaudeArgs_IncludesMCPConfig
```
Expected: PASS.

- [ ] **Step 5: Repeat for promptcapture**

Add to `internal/promptcapture/capture_test.go` (create if needed):
```go
func TestBuildClaudeArgs_IncludesMCPConfig(t *testing.T) {
    args := buildClaudeArgs(Opts{Prompt: "dump"})
    for i, a := range args {
        if a == "--mcp-config" && i+1 < len(args) && args[i+1] == "/etc/eidos/mcp.json" {
            return
        }
    }
    t.Fatal("--mcp-config /etc/eidos/mcp.json not present in argv")
}
```

Run: `go test ./internal/promptcapture/... -run TestBuildClaudeArgs_IncludesMCPConfig` → FAIL.

Modify `internal/promptcapture/capture.go` `buildClaudeArgs` (around line 150) to insert before the trailing `-p` push:
```go
    args = append(args, "--dangerously-skip-permissions")
    args = append(args, "--mcp-config", "/etc/eidos/mcp.json")
    args = append(args, opts.ExtraArgs...)
    args = append(args, "-p", opts.Prompt)
```

Run: `go test ./internal/promptcapture/...` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agentloop/spawn.go internal/agentloop/spawn_test.go internal/promptcapture/capture.go internal/promptcapture/capture_test.go
git commit -m "feat(mcp): wire --mcp-config into agent-loop and prompt-capture spawns"
```

---

## Task 15: System prompt update

**Files:**
- Modify: `internal/prompts/assets/system1-instructions.txt` (lines 36-38)

- [ ] **Step 1: Replace the placeholder paragraph**

Open `internal/prompts/assets/system1-instructions.txt`. Find the section starting at line 36:

```
## eidos-mcp tools

(To be added: tools for sending messages through MindGate, querying contacts, adjusting your own scheduling. For now use the host CLI via Bash where needed.)
```

Replace the parenthetical placeholder with the introduction paragraph. The exact text to substitute (everything between `## eidos-mcp tools` and the next heading):

```
You have a set of MCP tools, all prefixed `eidos_`, that operate on your MindForge framework state directly. Use them in preference to `Bash` + `eidos gate ...`: they reach the same daemon underneath, but the typed surface is friendlier for tool calls.

- **Messaging:** `eidos_send` to send a MindGate message; `eidos_inbox_list` and `eidos_outbox_list` to inspect message history.
- **Contacts:** `eidos_contact_add` / `_add_from_card`, `_remove`, `_set_label`, `_set_tier`. `eidos_card_scan` parses a `mindgate://...` URI and tells you whether the npub is already a contact.
- **Invites:** `eidos_invite_create` / `_list` / `_revoke`.
- **Relays:** `eidos_relay_add` / `_remove`.
- **Self:** `eidos_set_label` to change your advertised label; `eidos_config_set` to adjust a registered config key; `eidos_agent_state` to see your own claude-busy flag.
- **State reads:** `eidos_state_get <path>` is the unified read tool — `state.get contacts`, `state.get config.heartbeat.interval`, `state.get inbox.recent`, and so on. Use it instead of asking the filesystem for what the daemon already knows.

Write operations are validated against the daemon's Context / key registry. If you try something that belongs to the host side, you will get a clear `[CONTEXT_MISMATCH]` error with a verb hint — propagate it to the operator if needed, do not try to work around it.
```

- [ ] **Step 2: Verify the prompt still renders (smoke test)**

If a test exists at `internal/prompts/system_test.go`, run it:
```bash
go test ./internal/prompts/...
```
Expected: PASS.

If no test exists, build the binary and run a `forge prompt-dump` against any existing mind-form to eyeball the rendered prompt later (deferred to Task 17 integration).

- [ ] **Step 3: Commit**

```bash
git add internal/prompts/assets/system1-instructions.txt
git commit -m "feat(mcp): document eidos-mcp tool surface in mind-form system prompt"
```

---

## Task 16: Operator documentation in USAGE.md

**Files:**
- Modify: `docs/USAGE.md`

- [ ] **Step 1: Locate the right section to extend**

Open `docs/USAGE.md`. Skim for an existing "Operator host tools", "Developer setup", or similar section. If none exists, add a new top-level section near the end.

- [ ] **Step 2: Add the host-operator snippet**

Append (or insert into the appropriate section):

````markdown
## Using eidos-mcp from your host Claude Code

The same `eidos mcp` subcommand that the mind-form's claude uses can be wired into your own Claude Code session on the host. This gives you the same typed tool surface (`eidos_send`, `eidos_state_get`, contact / invite / relay management) for the gate daemon running on your workstation.

Add this to your project-local `.mcp.json` (or `~/.claude.json`):

```json
{
  "mcpServers": {
    "eidos": {
      "command": "eidos",
      "args": ["mcp"]
    }
  }
}
```

`eidos mcp` resolves the gate socket from `$EIDOS_GATE_HOME` (or your platform default — usually `~/.config/eidos/sock`), the same way `eidos gate ...` does. Tools that only make sense inside a mind-form container (`eidos_agent_state`, `eidos_lifecycle_status`) will return `[CONTEXT_MISMATCH]` from your host daemon — that's expected.
````

- [ ] **Step 3: Commit**

```bash
git add docs/USAGE.md
git commit -m "docs(mcp): operator-host setup snippet for eidos-mcp"
```

---

## Task 17: Integration test against real daemon

**Files:**
- Create: `internal/mcp/integration_test.go` (build tag `integration`)

This is the end-to-end check: real `eidos gate` daemon listening on a temp socket, real `eidos mcp` subprocess speaking MCP over stdio, real tool calls round-tripping through the daemon's method table.

- [ ] **Step 1: Write the integration test scaffold**

Create `internal/mcp/integration_test.go`:
```go
//go:build integration

package mcp_test

import (
    "context"
    "os/exec"
    "path/filepath"
    "testing"
    "time"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestEidosMCP_EndToEnd_SetLabel_StateGet boots a real gate daemon on
// a temp socket, starts `eidos mcp` as a child via the SDK's stdio
// client, invokes eidos_set_label, then eidos_state_get('identity'),
// and asserts the label round-trips.
func TestEidosMCP_EndToEnd_SetLabel_StateGet(t *testing.T) {
    dir := t.TempDir()

    // 1. Start a real gate daemon in a goroutine, pointed at `dir`.
    //    Reuse the existing test helper in internal/daemon/testhelpers.go
    //    or, if none exists, fall back to spawning `eidos gate daemon
    //    --state-dir <dir>` as a subprocess.
    daemonCmd := exec.Command("go", "run", "./cmd/eidos", "gate", "daemon", "--state-dir", dir)
    daemonCmd.Dir = "/data/eidopsyche"
    if err := daemonCmd.Start(); err != nil {
        t.Fatalf("start daemon: %v", err)
    }
    defer func() {
        _ = daemonCmd.Process.Kill()
        _ = daemonCmd.Wait()
    }()

    // 2. Wait for the socket to appear (up to 5s).
    socket := filepath.Join(dir, "sock")
    deadline := time.Now().Add(5 * time.Second)
    for time.Now().Before(deadline) {
        if _, err := exec.Command("test", "-S", socket).Output(); err == nil {
            break
        }
        time.Sleep(50 * time.Millisecond)
    }

    // 3. Build `eidos` binary once for the MCP child.
    bin := filepath.Join(t.TempDir(), "eidos")
    out, err := exec.Command("go", "build", "-o", bin, "./cmd/eidos").CombinedOutput()
    if err != nil {
        t.Fatalf("build eidos: %v\n%s", err, out)
    }

    // 4. Connect to `eidos mcp --state-dir <dir>` via the SDK client.
    transport := &mcp.CommandTransport{Command: exec.Command(bin, "mcp", "--state-dir", dir)}
    client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    session, err := client.Connect(ctx, transport, nil)
    if err != nil {
        t.Fatalf("mcp connect: %v", err)
    }
    defer session.Close()

    // 5. List tools and assert eidos_set_label + eidos_state_get exist.
    tools, err := session.ListTools(ctx, nil)
    if err != nil {
        t.Fatalf("list tools: %v", err)
    }
    need := map[string]bool{"eidos_set_label": true, "eidos_state_get": true}
    for _, tool := range tools.Tools {
        delete(need, tool.Name)
    }
    if len(need) > 0 {
        t.Fatalf("missing tools: %v", need)
    }

    // 6. Call eidos_set_label.
    if _, err := session.CallTool(ctx, &mcp.CallToolParams{
        Name:      "eidos_set_label",
        Arguments: map[string]any{"label": "integration-test"},
    }); err != nil {
        t.Fatalf("set_label: %v", err)
    }

    // 7. Call eidos_state_get and assert the label is set.
    res, err := session.CallTool(ctx, &mcp.CallToolParams{
        Name:      "eidos_state_get",
        Arguments: map[string]any{"path": "identity.label"},
    })
    if err != nil {
        t.Fatalf("state_get: %v", err)
    }
    if res.IsError {
        t.Fatalf("state_get returned IsError: %+v", res.Content)
    }
    // The structured content carries the label string. Cast accordingly.
    // Adjust the assertion shape to the SDK's actual Content[0] type.
}
```

**Note on the SDK transport name** (`CommandTransport`): the official
SDK exposes its stdio-child transport under a specific name; the
implementer must verify against the pinned SDK version. If the actual
name differs (e.g. `StdioClientTransport{Command: ...}`), update
accordingly — the principle is "the SDK's documented stdio client
transport".

**Note on daemon boot helper:** if `cmd/eidos/gate` has an existing
test helper (search `internal/daemon/testdaemon.go` or
`gate/daemon_test.go`), prefer it over re-running the binary —
faster and fewer moving parts. The `go run` invocation above is the
fallback that works without a helper.

- [ ] **Step 2: Run the test**

Run:
```bash
cd /data/eidopsyche
go test -tags=integration ./internal/mcp/... -run TestEidosMCP_EndToEnd -v
```
Expected: PASS (allow ~20s for daemon boot + binary build). If it
fails on the SDK transport name, fix and re-run.

- [ ] **Step 3: Add to CI integration matrix**

Verify `.github/workflows/ci.yml` already runs `go test -tags=integration ./...`. If yes, this test is automatically included. If no, the integration step needs to be added — out of scope for this task; raise as a follow-up.

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/integration_test.go
git commit -m "test(mcp): end-to-end integration test against real gate daemon"
```

---

## Task 18: Final manual verification

**Files:** none (verification only)

- [ ] **Step 1: Rebuild the mind-form image with the new code**

Run:
```bash
cd /data/eidopsyche
make image
```
Expected: build succeeds.

- [ ] **Step 2: Spin up a test mind-form**

Run:
```bash
go build -o bin/eidos ./cmd/eidos
./bin/eidos forge create mcptest
./bin/eidos forge start mcptest
```
Expected: mind-form boots without errors. Allow ~10s for the agent-loop to start its first session.

- [ ] **Step 3: Inspect the prompt-dump for the MCP catalogue**

Run:
```bash
./bin/eidos forge prompt-dump mcptest > /tmp/mcptest-dump.json
```

Verify the `mcp_servers` field includes `eidos` and the `tools` array includes the `eidos_*` names. The exact path inside the dump depends on the prompt-dump envelope shape (`internal/transcript/events.go`'s `MCPServers` field).

- [ ] **Step 4: Watch the agent-loop and confirm a tool call**

Run:
```bash
./bin/eidos forge watch mcptest
```

In another terminal, send the mind-form a chat message that nudges it toward a state read:
```bash
./bin/eidos forge send mcptest "Use eidos_state_get to read your identity, then tell me your npub."
```

Expected: watch shows the agent calling `eidos_state_get` (not `Bash` shellout) and replying with the npub.

- [ ] **Step 5: Clean up**

```bash
./bin/eidos forge stop mcptest
./bin/eidos forge remove mcptest
rm /tmp/mcptest-dump.json
```

- [ ] **Step 6: Run the full local CI parity check**

Run:
```bash
gofmt -l . && go vet ./... && go test ./... && go test -tags=integration ./...
```
Expected: all clean. This mirrors what the CI workflow runs.

(No commit for this task — it is verification only.)

---

## Self-Review Notes

**Spec coverage check (against the design doc):**

- ✅ Library choice (go-sdk) → Task 1
- ✅ stdio transport + cobra entry → Task 2
- ✅ IPC client wrapper + error translation → Task 3
- ✅ All 21 tools registered → Tasks 4-12
- ✅ Static full-set catalogue (no dynamic Context probing) → consistent across tasks
- ✅ Independent schema (Input/Output per tool) → every tool task
- ✅ `.mcp.json` baked at `/etc/eidos/mcp.json` → Task 13
- ✅ Spawn wiring at the 2 of 4 sites → Task 14 (with explicit exclusion of birth + firstcontact)
- ✅ System prompt update → Task 15
- ✅ USAGE.md operator section → Task 16
- ✅ Integration test → Task 17
- ✅ Manual smoke → Task 18

**Anti-feature reminders (not in the plan, intentionally):**
- No host-only / dangerous tool wrappers (forge.upgrade, daemon.exec-replace, etc.) — see "Deliberately not exposed" in spec.
- No state.changed push notifications — future work.
- No per-tool ACL — future work.

**Verification reminders:**
- Tool count: `grep -h "mcp.AddTool" internal/mcp/tools_*.go | wc -l` should equal `21` by end of Task 12.
- SDK API surface: verify `mcp.NewServer`, `mcp.AddTool`, `mcp.StdioTransport`, and the client-side `CommandTransport` / equivalent name against the actual SDK version pinned in `go.mod` — names above are the design intent; the implementer matches them to the pinned version.
