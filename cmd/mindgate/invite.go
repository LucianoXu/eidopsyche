package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

var (
	inviteSingleUse     bool
	inviteMaxUses       int
	inviteUnlimited     bool
	inviteExpires       string
	inviteNoExpiry      bool
	inviteIssuerLabel   string
	inviteRedeemerLabel string
	inviteListStatus    string
)

var inviteCmd = &cobra.Command{
	Use:   "invite",
	Short: "Create, list, and revoke invitation tokens",
}

var inviteCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new invitation token",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()

		params := map[string]any{
			"issuer_label":   inviteIssuerLabel,
			"redeemer_label": inviteRedeemerLabel,
		}
		switch {
		case inviteUnlimited:
			params["max_uses"] = 0
			params["unlimited"] = true
		case inviteMaxUses > 0:
			params["max_uses"] = inviteMaxUses
		default:
			params["single_use"] = true
		}
		switch {
		case inviteNoExpiry:
			params["expires_seconds"] = int64(-1)
		case inviteExpires != "":
			d, err := parseDuration(inviteExpires)
			if err != nil {
				return fmt.Errorf("invalid --expires value %q: %w", inviteExpires, err)
			}
			params["expires_seconds"] = int64(d.Seconds())
			// else default: daemon picks 7d
		}

		var resp struct {
			ID        string `json:"id"`
			URI       string `json:"uri"`
			ExpiresAt int64  `json:"expires_at"`
			MaxUses   int    `json:"max_uses"`
		}
		if err := mustOK(c.Call("invite.create", params, &resp)); err != nil {
			return err
		}
		// URI goes to stdout for pipe-friendliness.
		fmt.Println(resp.URI)
		// Metadata goes to stderr.
		if resp.ExpiresAt > 0 {
			fmt.Fprintf(os.Stderr, "id=%s  max_uses=%d  expires=%s\n",
				resp.ID[:12], resp.MaxUses,
				time.Unix(resp.ExpiresAt, 0).UTC().Format(time.RFC3339))
		} else {
			fmt.Fprintf(os.Stderr, "id=%s  max_uses=%d  expires=never\n",
				resp.ID[:12], resp.MaxUses)
		}
		return nil
	},
}

var inviteListCmd = &cobra.Command{
	Use:   "list",
	Short: "List invitation tokens",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()

		params := map[string]any{}
		if inviteListStatus != "" {
			params["status"] = inviteListStatus
		}

		var resp []struct {
			ID            string `json:"id"`
			CreatedAt     int64  `json:"created_at"`
			ExpiresAt     int64  `json:"expires_at"`
			MaxUses       int    `json:"max_uses"`
			Uses          int    `json:"uses"`
			Status        string `json:"status"`
			IssuerLabel   string `json:"issuer_label"`
			RedeemerLabel string `json:"redeemer_label"`
		}
		if err := mustOK(c.Call("invite.list", params, &resp)); err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSTATUS\tUSES/MAX\tEXPIRES\tISSUER\tREDEEMER")
		for _, inv := range resp {
			shortID := inv.ID
			if len(shortID) > 16 {
				shortID = shortID[:16]
			}
			maxStr := strconv.Itoa(inv.MaxUses)
			if inv.MaxUses == 0 {
				maxStr = "∞"
			}
			expStr := "never"
			if inv.ExpiresAt > 0 {
				expStr = time.Unix(inv.ExpiresAt, 0).UTC().Format("2006-01-02 15:04")
			}
			fmt.Fprintf(w, "%s\t%s\t%d/%s\t%s\t%s\t%s\n",
				shortID, inv.Status, inv.Uses, maxStr, expStr,
				inv.IssuerLabel, inv.RedeemerLabel)
		}
		return w.Flush()
	},
}

var inviteRevokeCmd = &cobra.Command{
	Use:   "revoke <id-prefix>",
	Short: "Revoke an invitation by ID prefix",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		var resp struct {
			OK     bool   `json:"ok"`
			FullID string `json:"full_id"`
		}
		if err := mustOK(c.Call("invite.revoke", map[string]string{"id_prefix": args[0]}, &resp)); err != nil {
			return err
		}
		fmt.Printf("revoked: %s\n", resp.FullID)
		return nil
	},
}

func init() {
	inviteCreateCmd.Flags().BoolVar(&inviteSingleUse, "single-use", false, "single-use invite (default if none of --max-uses/--unlimited given)")
	inviteCreateCmd.Flags().IntVar(&inviteMaxUses, "max-uses", 0, "max number of redemptions before exhaustion")
	inviteCreateCmd.Flags().BoolVar(&inviteUnlimited, "unlimited", false, "no use limit")
	inviteCreateCmd.Flags().StringVar(&inviteExpires, "expires", "", "duration before expiry, e.g. 7d, 24h, 30m (default 7d)")
	inviteCreateCmd.Flags().BoolVar(&inviteNoExpiry, "no-expiry", false, "never expires")
	inviteCreateCmd.Flags().StringVar(&inviteIssuerLabel, "issuer-label", "", "label the redeemer pre-fills for you")
	inviteCreateCmd.Flags().StringVar(&inviteRedeemerLabel, "redeemer-label", "", "label you'll give the redeemer")

	inviteListCmd.Flags().StringVar(&inviteListStatus, "status", "", "filter by status: active, expired, revoked (default: all)")

	inviteCmd.AddCommand(inviteCreateCmd, inviteListCmd, inviteRevokeCmd)
	rootCmd.AddCommand(inviteCmd)
}

// parseDuration accepts standard time.ParseDuration tokens plus "Nd" for days.
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
