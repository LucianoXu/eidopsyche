package forge

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)


func newCreateCmd() *cobra.Command {
	o := CreateOpts{}
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a mind-form (image pull + volume + ontology + key + login)",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("mind-form name is required (usage: eidos forge create <name>)")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := forgectl.ValidateName(name); err != nil {
				return err
			}
			if err := validateOwner(o.Owner); err != nil {
				return err
			}
			if err := validateRelay(o.Relay); err != nil {
				return err
			}
			if err := validateModel(o.Model); err != nil {
				return err
			}
			if err := validateHeartbeatInterval(o.HeartbeatInterval); err != nil {
				return err
			}
			if o.Label == "" {
				o.Label = name
			}
			return runCreate(cmd, name, o)
		},
	}
	cmd.Flags().StringVar(&o.Owner, "owner", "", "master human's npub (required)")
	cmd.Flags().StringVar(&o.Relay, "relay", "", "relay URL the mind-form publishes/subscribes to (required)")
	cmd.Flags().StringVar(&o.Label, "label", "", "human-readable label (default: <name>)")
	cmd.Flags().BoolVar(&o.NoLogin, "no-login", false, "skip the interactive claude /login step")
	cmd.Flags().StringVar(&o.Image, "image", "", "override container image (default: pinned in this binary)")
	cmd.Flags().StringVar(&o.Model, "model", "", "claude model id to pin (e.g. claude-sonnet-4-7); empty = claude default")
	cmd.Flags().StringVar(&o.HeartbeatInterval, "heartbeat-interval", "",
		"HeartBeat cadence (e.g. 2m, 30m, 2h); empty = 2h default. Supported: 1m,2m,3m,4m,5m,6m,10m,12m,15m,20m,30m,1h,2h,3h,4h,6h,8h,12h,24h")
	return cmd
}

func validateModel(s string) error {
	return config.ValidateModelID(s)
}

func validateHeartbeatInterval(s string) error {
	return config.ValidateHeartbeatInterval(s)
}

func validateOwner(s string) error {
	if s == "" {
		return fmt.Errorf("--owner is required")
	}
	if !strings.HasPrefix(s, "npub1") || len(s) < 10 {
		return fmt.Errorf("--owner must be a valid npub (got %q)", s)
	}
	return nil
}

func validateRelay(s string) error {
	if s == "" {
		return fmt.Errorf("--relay is required")
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return fmt.Errorf("--relay is not a valid URL: %q", s)
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return fmt.Errorf("--relay must be ws:// or wss:// (got %q)", u.Scheme)
	}
	return nil
}

// runCreate is the orchestration entry point.
func runCreate(cmd *cobra.Command, name string, o CreateOpts) error {
	return runCreate2(cmd, name, o)
}
