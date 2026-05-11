package forge

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)

// CreateOpts is the inputs to Orchestrate. All flag-bound CLI options
// land here, and the First Contact wizard fills it in from in-memory
// summoning state. The wizard-only fields KeyHex and JournalEntry are
// not exposed as CLI flags — they make no sense for scripted use.
type CreateOpts struct {
	Owner   string
	Relay   string
	Label   string
	NoLogin bool
	Image   string
	Model   string

	// HeartbeatInterval is the per-mind-form HeartBeat cadence written
	// into /eidos/gate/config.toml at init-volume time. Empty leaves
	// the [heartbeat] block unset, so the supervisor falls back to
	// config.DefaultHeartbeatInterval. Must be in the supported set
	// (validated via config.ValidateHeartbeatInterval).
	HeartbeatInterval string

	KeyHex       string // wizard-only: pre-generated MindForm private hex
	JournalEntry string // wizard-only: rendered summoning-book markdown

	// PrefabID, when non-empty, makes Orchestrate stream the
	// prefab/<id>/ tree into the volume instead of the canonical
	// template. The wizard's Phase 3 prefab branch sets this; the
	// scratch path leaves it empty.
	PrefabID string

	// OwnerLabel is the master's human-readable label, surfaced to
	// prefab .tpl files (e.g. summoning-book templates). Empty on
	// scratch path; the scratch template/ does not reference it.
	OwnerLabel string

	// MindFormNpub is the new mind-form's npub, surfaced to prefab
	// .tpl files. The wizard knows it after key generation; CLI
	// `eidos forge create` (which has no key context) leaves it
	// empty — prefab path is wizard-only.
	MindFormNpub string
}

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
