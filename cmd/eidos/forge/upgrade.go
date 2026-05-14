package forge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
	"github.com/spf13/cobra"
)

type upgradeFlags struct {
	Image       string
	WaitIdle    bool
	IdleTimeout string
	Grace       int
	DryRun      bool
	NonTTY      bool // test seam: forces auto-confirm without checking stdin
}

func newUpgradeCmd() *cobra.Command {
	var f upgradeFlags
	cmd := &cobra.Command{
		Use:   "upgrade <name>",
		Short: "Upgrade a mind-form's container to a newer image (volume preserved)",
		Long: `Pulls the target image, prints the version diff (eidos + claude-code),
then stops the mind-form, recreates its container on the new image
with the same volume, and starts it back up.

The volume is preserved. Identity, contacts, ontology, and Claude auth
state all survive. The mind-form's image switches; nothing else does.

Use --dry-run to inspect the version diff without mutating the
container.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := forgectl.ValidateName(args[0]); err != nil {
				return err
			}
			return runUpgradeWithIPC(cmd.OutOrStdout(), &realUpgradeIPC{}, args[0], f)
		},
	}
	cmd.Flags().StringVar(&f.Image, "image", "", "override container image (default: pinned in this binary)")
	cmd.Flags().BoolVar(&f.WaitIdle, "wait-idle", false, "wait for agentloop to be idle before stopping")
	cmd.Flags().StringVar(&f.IdleTimeout, "idle-timeout", "10m", "max time to wait for idle (Go duration; only with --wait-idle)")
	cmd.Flags().IntVar(&f.Grace, "grace", 10, "seconds to wait before SIGKILL when stopping")
	cmd.Flags().BoolVar(&f.DryRun, "dry-run", false, "show the version diff and exit; do not mutate the container")
	return cmd
}

// upgradeIPC is the test seam over the host gate daemon's
// forge.upgrade IPC method.
type upgradeIPC interface {
	Call(method string, params ipc.ForgeUpgradeParams) (ipc.ForgeUpgradeResult, *ipc.Error)
}

// realUpgradeIPC dials the host gate daemon and dispatches the call.
type realUpgradeIPC struct{}

func (r *realUpgradeIPC) Call(method string, params ipc.ForgeUpgradeParams) (ipc.ForgeUpgradeResult, *ipc.Error) {
	return callDaemonForgeUpgrade(context.Background(), method, params)
}

// callDaemonForgeUpgrade dials the host gate daemon unix socket and
// dispatches the forge.upgrade IPC method. It follows the same pattern
// as cmd/eidos/gate/client.go:newClient().
func callDaemonForgeUpgrade(ctx context.Context, method string, params ipc.ForgeUpgradeParams) (ipc.ForgeUpgradeResult, *ipc.Error) {
	dir, err := config.ResolveStateDir("")
	if err != nil {
		return ipc.ForgeUpgradeResult{}, &ipc.Error{Code: ipc.ErrInternal, Message: "resolve state dir: " + err.Error()}
	}
	cfg, _ := config.Load(filepath.Join(dir, "config.toml"))
	socket := filepath.Join(dir, cfg.Daemon.Socket)
	c, err := ipc.Dial(socket)
	if err != nil {
		return ipc.ForgeUpgradeResult{}, &ipc.Error{Code: ipc.ErrInternal, Message: fmt.Sprintf("daemon not running (socket %s): %v", socket, err)}
	}
	defer c.Close()

	rawParams, err := json.Marshal(params)
	if err != nil {
		return ipc.ForgeUpgradeResult{}, &ipc.Error{Code: ipc.ErrInternal, Message: "marshal params: " + err.Error()}
	}

	_ = ctx // ipc.Client.Call does not take a context yet; ctx reserved for future use
	var res ipc.ForgeUpgradeResult
	ipcErr, callErr := c.Call(method, json.RawMessage(rawParams), &res)
	if callErr != nil {
		return ipc.ForgeUpgradeResult{}, &ipc.Error{Code: ipc.ErrInternal, Message: "call: " + callErr.Error()}
	}
	if ipcErr != nil {
		return ipc.ForgeUpgradeResult{}, ipcErr
	}
	return res, nil
}

// runUpgradeWithIPC is the testable entry point. Tests inject a fake IPC.
func runUpgradeWithIPC(out io.Writer, c upgradeIPC, name string, f upgradeFlags) error {
	preview, ierr := c.Call("forge.upgrade", ipc.ForgeUpgradeParams{
		Name:        name,
		Image:       f.Image,
		WaitIdle:    f.WaitIdle,
		IdleTimeout: f.IdleTimeout,
		Grace:       f.Grace,
		DryRun:      true,
	})
	if ierr != nil {
		return fmt.Errorf("preview: %s: %s", ierr.Code, ierr.Message)
	}

	renderDiff(out, preview)

	if preview.Skipped {
		return nil
	}
	if f.DryRun {
		return nil
	}

	if !f.NonTTY && isTTY() {
		if !confirm(out, "proceed? [Y/n] ") {
			fmt.Fprintln(out, "aborted")
			return nil
		}
	}

	live, ierr := c.Call("forge.upgrade", ipc.ForgeUpgradeParams{
		Name:        name,
		Image:       f.Image,
		WaitIdle:    f.WaitIdle,
		IdleTimeout: f.IdleTimeout,
		Grace:       f.Grace,
		DryRun:      false,
	})
	if ierr != nil {
		return fmt.Errorf("upgrade: %s: %s", ierr.Code, ierr.Message)
	}
	fmt.Fprintf(out, "✓ %s upgraded", name)
	if live.NewEidos != "" {
		fmt.Fprintf(out, " to %s", live.NewEidos)
	}
	fmt.Fprintln(out)
	return nil
}

func renderDiff(out io.Writer, r ipc.ForgeUpgradeResult) {
	fmt.Fprintf(out, "upgrading %s:\n", r.Name)
	fmt.Fprintf(out, "  image      : %s\n             → %s\n", orFallback(r.OldImage, "<absent>"), r.NewImage)
	fmt.Fprintf(out, "  eidos      : %s → %s\n", orFallback(r.OldEidos, "unknown"), orFallback(r.NewEidos, "unknown"))
	fmt.Fprintf(out, "  claude-code: %s → %s\n", orFallback(r.OldClaudeCode, "unknown"), orFallback(r.NewClaudeCode, "unknown"))
	if r.Skipped {
		fmt.Fprintf(out, "  (no-op: %s)\n", r.SkippedReason)
	}
}

func orFallback(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func isTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func confirm(out io.Writer, prompt string) bool {
	fmt.Fprint(out, prompt)
	br := bufio.NewReader(os.Stdin)
	line, err := br.ReadString('\n')
	if err != nil {
		return false
	}
	resp := strings.ToLower(strings.TrimSpace(line))
	return resp == "" || resp == "y" || resp == "yes"
}
