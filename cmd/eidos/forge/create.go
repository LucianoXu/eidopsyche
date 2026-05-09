package forge

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)

type createOpts struct {
	owner   string
	relay   string
	label   string
	noLogin bool
	image   string
	model   string
}

func newCreateCmd() *cobra.Command {
	o := createOpts{}
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
			if err := validateOwner(o.owner); err != nil {
				return err
			}
			if err := validateRelay(o.relay); err != nil {
				return err
			}
			if err := validateModel(o.model); err != nil {
				return err
			}
			if o.label == "" {
				o.label = name
			}
			return runCreate(cmd, name, o)
		},
	}
	cmd.Flags().StringVar(&o.owner, "owner", "", "master human's npub (required)")
	cmd.Flags().StringVar(&o.relay, "relay", "", "relay URL the mind-form publishes/subscribes to (required)")
	cmd.Flags().StringVar(&o.label, "label", "", "human-readable label (default: <name>)")
	cmd.Flags().BoolVar(&o.noLogin, "no-login", false, "skip the interactive claude /login step")
	cmd.Flags().StringVar(&o.image, "image", "", "override container image (default: pinned in this binary)")
	cmd.Flags().StringVar(&o.model, "model", "", "claude model id to pin (e.g. claude-sonnet-4-7); empty = claude default")
	return cmd
}

func validateModel(s string) error {
	return config.ValidateModelID(s)
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
func runCreate(cmd *cobra.Command, name string, o createOpts) error {
	return runCreate2(cmd, name, o)
}
