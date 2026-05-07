package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/version"
)

// CommandHandler executes one envelope-v1 command and returns the reply text.
// The reply is wrapped by the daemon into a type=chat envelope and sent back
// to the original sender (which, given the §5.1 authority rule, is self).
type CommandHandler func(ctx context.Context, d *Daemon, args map[string]any) (string, error)

// commandRegistry is the static map of v1 command names to their handlers.
// New commands are registered here. Adding a command does NOT bump the
// envelope schema version; new names are additive within v1.
var commandRegistry = map[string]CommandHandler{
	"status": statusCommand,
}

// dispatchCommand looks up name in the registry and invokes the handler.
// Returns ("", false, nil) if name is unknown — the caller should treat
// this as envelope §6 reason "unknown_command" and soft-reject.
func dispatchCommand(ctx context.Context, d *Daemon, name string, args map[string]any) (reply string, ok bool, err error) {
	h, found := commandRegistry[name]
	if !found {
		return "", false, nil
	}
	out, err := h(ctx, d, args)
	return out, true, err
}

// statusCommand returns a human-readable summary of daemon health.
func statusCommand(ctx context.Context, d *Daemon, _ map[string]any) (string, error) {
	uptime := time.Since(d.startedAt).Round(time.Second)

	all, err := d.Repo.List(ctx)
	if err != nil {
		return "", fmt.Errorf("list contacts: %w", err)
	}
	tierCount := map[contacts.Tier]int{}
	for _, c := range all {
		tierCount[c.Tier]++
	}

	relayLine := "relays:   ? configured (db error)\n"
	var relayN int
	if err := d.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM own_relays`).Scan(&relayN); err == nil {
		relayLine = fmt.Sprintf("relays:   %d configured\n", relayN)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "eidos-gate %s  uptime %s\n", version.Version, uptime)
	fmt.Fprintf(&sb, "contacts: %d master, %d friend, %d acquaintance, %d blocked\n",
		tierCount[contacts.TierMaster],
		tierCount[contacts.TierFriend],
		tierCount[contacts.TierAcquaintance],
		tierCount[contacts.TierBlocked])
	sb.WriteString(relayLine)
	fmt.Fprintf(&sb, "forge:    n/a (forge subcommand not yet integrated)")
	return sb.String(), nil
}
