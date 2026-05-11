package gate

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// stateCmd exposes the single read interface to operators and to
// mindforms inside a container. It mirrors the daemon's `state.get`
// IPC method: optional dotted path argument selects a subtree;
// missing arg returns the full root snapshot. Output is pretty-printed
// JSON so downstream tooling (jq, dashboards) can consume it.
//
// Examples:
//
//	eidos gate state                         # full snapshot
//	eidos gate state config.heartbeat.interval
//	eidos gate state contacts
//	eidos gate state identity
var stateCmd = &cobra.Command{
	Use:   "state [path]",
	Short: "Print state subtree (dotted path) or full snapshot",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runState,
}

func init() { rootCmd.AddCommand(stateCmd) }

func runState(_ *cobra.Command, args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	defer c.Close()

	params := map[string]string{}
	if len(args) == 1 {
		params["path"] = args[0]
	}
	var out any
	if err := mustOK(c.Call("state.get", params, &out)); err != nil {
		return err
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(body))
	return nil
}
