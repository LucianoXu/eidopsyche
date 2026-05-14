package forge

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage a mind-form's shared workspace mounts",
		Long: `Workspaces are host directories bind-mounted into a mind-form's
container at /workspace/<name>/. Changes take effect on the next
'eidos forge restart <mindform>'.`,
	}
	cmd.AddCommand(newWorkspaceAddCmd(), newWorkspaceRemoveCmd(), newWorkspaceListCmd())
	return cmd
}

func newWorkspaceAddCmd() *cobra.Command {
	var mode string
	var noWarnUID bool
	cmd := &cobra.Command{
		Use:   "add <mindform> <name> <host-path>",
		Short: "Bind a host directory into a mind-form's container",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newIPCClient()
			if err != nil {
				return err
			}
			defer c.Close()
			var resp struct {
				PendingRestart bool     `json:"pending_restart"`
				Warnings       []string `json:"warnings"`
			}
			err = mustOK(c.Call("forge.workspace.add", map[string]any{
				"mindform":    args[0],
				"name":        args[1],
				"host_path":   args[2],
				"mode":        mode,
				"no_warn_uid": noWarnUID,
			}, &resp))
			if err != nil {
				return err
			}
			for _, w := range resp.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}
			if resp.PendingRestart {
				cmd.Printf("added %s to %s (pending restart — run 'eidos forge restart %s' to apply)\n", args[1], args[0], args[0])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "", "mount mode: ro or rw (default rw)")
	cmd.Flags().BoolVar(&noWarnUID, "no-warn-uid", false, "suppress the uid-mismatch warning")
	return cmd
}

func newWorkspaceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <mindform> <name>",
		Short: "Remove a workspace binding from a mind-form's config",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newIPCClient()
			if err != nil {
				return err
			}
			defer c.Close()
			var resp struct {
				PendingRestart bool `json:"pending_restart"`
			}
			err = mustOK(c.Call("forge.workspace.remove", map[string]any{
				"mindform": args[0],
				"name":     args[1],
			}, &resp))
			if err != nil {
				return err
			}
			cmd.Printf("removed %s from %s (pending restart — run 'eidos forge restart %s' to apply)\n", args[1], args[0], args[0])
			return nil
		},
	}
}

func newWorkspaceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <mindform>",
		Short: "List a mind-form's configured workspaces",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newIPCClient()
			if err != nil {
				return err
			}
			defer c.Close()
			var resp struct {
				Desired []struct {
					Name     string `json:"name"`
					HostPath string `json:"host_path"`
					Mode     string `json:"mode"`
				} `json:"desired"`
				Actual []struct {
					Name     string `json:"name"`
					HostPath string `json:"host_path"`
					Mode     string `json:"mode"`
				} `json:"actual"`
				PendingRestart bool `json:"pending_restart"`
			}
			err = mustOK(c.Call("forge.workspace.list", map[string]any{
				"mindform": args[0],
			}, &resp))
			if err != nil {
				return err
			}
			cmd.Printf("workspaces for %s:\n", args[0])
			if len(resp.Desired) == 0 {
				cmd.Println("  (none)")
			} else {
				for _, w := range resp.Desired {
					cmd.Printf("  %s\t%s\t%s\n", w.Name, w.Mode, w.HostPath)
				}
			}
			// Pending restart can be true even when Desired is empty: the
			// operator may have just removed the last workspace and the
			// container still has the old bind mount. Always surface the
			// hint so they don't think removal is already in effect.
			if resp.PendingRestart {
				cmd.Printf("\n⚠ pending restart — run 'eidos forge restart %s' to apply\n", args[0])
			}
			return nil
		},
	}
}
