package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// WorkspaceMount is one entry in a mind-form's bind-mount list. The
// container target is always /workspace/<Name>/; only Name, HostPath,
// and Mode are persisted.
type WorkspaceMount struct {
	Name     string `toml:"name"`
	HostPath string `toml:"host_path"`
	// Mode is "ro" or "rw". Empty in config.toml resolves to "rw" at
	// read time via WorkspaceMount.EffectiveMode; we keep the persisted
	// zero value empty so an absent field round-trips cleanly.
	Mode string `toml:"mode,omitempty"`
}

// ForgeMindForm is the host-gate per-mind-form section
// [forge.<name>]. Only Workspaces lives here today; future per-mind-form
// host-side knobs join this struct.
type ForgeMindForm struct {
	Workspaces []WorkspaceMount `toml:"workspaces,omitempty"`
}

// EffectiveMode returns "rw" when Mode is empty, otherwise the
// persisted value. Used by every reader that needs to materialize the
// mount; do not re-implement.
func (w WorkspaceMount) EffectiveMode() string {
	if w.Mode == "" {
		return "rw"
	}
	return w.Mode
}

var workspaceNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidateWorkspaceName enforces the persisted name regex. The name
// becomes the last segment of the container path /workspace/<name>/.
func ValidateWorkspaceName(name string) error {
	if name == "" {
		return fmt.Errorf("workspace name must not be empty")
	}
	if !workspaceNameRE.MatchString(name) {
		return fmt.Errorf("workspace name %q does not match %s", name, workspaceNameRE.String())
	}
	return nil
}

// ValidateWorkspaceMode permits "", "ro", "rw". Empty resolves to "rw"
// at read time (see EffectiveMode). Case-sensitive.
func ValidateWorkspaceMode(mode string) error {
	switch mode {
	case "", "ro", "rw":
		return nil
	default:
		return fmt.Errorf("workspace mode %q: want \"ro\" or \"rw\"", mode)
	}
}

// ValidateWorkspaceHostPath stats the host path and enforces the
// absolute-clean-directory-exists rule. Callers should call this
// before persisting an add. Returned errors are operator-readable.
func ValidateWorkspaceHostPath(p string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("host_path %q must be absolute", p)
	}
	if filepath.Clean(p) != p {
		return fmt.Errorf("host_path %q must be cleaned (no .., no trailing slash, no double slashes)", p)
	}
	st, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("host_path %q: %w", p, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("host_path %q is not a directory", p)
	}
	return nil
}

// dangerousPathRules is the ordered list of (matcher, message) pairs
// the warning path uses. Order matters: docker.sock catches
// /var/run/docker.sock before /var/ would (it would not, but ordering
// keeps the special-case message first).
var dangerousPathRules = []struct {
	match func(string) bool
	msg   string
}{
	{
		match: func(p string) bool { return p == "/" },
		msg:   "mounting / exposes the entire host filesystem to the mind-form",
	},
	{
		match: func(p string) bool { return strings.HasSuffix(p, "/docker.sock") },
		msg:   "mounting the docker socket gives the mind-form full control over the host's docker daemon, including breaking out of its container",
	},
	{
		match: func(p string) bool { return strings.HasSuffix(p, "/.ssh") || strings.Contains(p, "/.ssh/") },
		msg:   "mounting an SSH key directory exposes private keys to the mind-form",
	},
	{
		match: func(p string) bool {
			return strings.HasSuffix(p, "/.config/eidos") || strings.Contains(p, "/.config/eidos/")
		},
		msg: "mounting eidos host config; the mind-form will be able to modify other mind-forms' configurations",
	},
	{
		match: func(p string) bool {
			return p == "/proc" || strings.HasPrefix(p, "/proc/") ||
				p == "/sys" || strings.HasPrefix(p, "/sys/") ||
				p == "/dev" || strings.HasPrefix(p, "/dev/")
		},
		msg: "mounting a kernel virtual filesystem exposes host kernel state to the mind-form",
	},
	{
		match: func(p string) bool { return p == "/etc" || strings.HasPrefix(p, "/etc/") },
		msg:   "mounting host system configuration",
	},
}

// DangerousHostPath returns a non-empty warning message if p matches
// one of the dangerous-path heuristics, otherwise "". The IPC handler
// emits the message as a non-blocking warning so the operator can
// proceed but is alerted.
func DangerousHostPath(p string) string {
	for _, rule := range dangerousPathRules {
		if rule.match(p) {
			return rule.msg
		}
	}
	return ""
}

// MindFormContainerUID is the uid the eidos user owns inside the
// mind-form container; see docker/mindform/Dockerfile:73. Hard-coded
// because the Dockerfile is the source of truth.
const MindFormContainerUID uint32 = 1000

// HostPathOwnerUID returns the owning uid of p on the host. The
// daemon's add handler uses this to emit a uid-mismatch warning.
func HostPathOwnerUID(p string) (uint32, error) {
	st, err := os.Stat(p)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", p, err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("stat %s: cannot read owner uid (non-Unix?)", p)
	}
	return sys.Uid, nil
}

// UIDMismatchWarning composes the operator-facing warning when an
// rw-mode workspace's host path is not owned by uid 1000.
func UIDMismatchWarning(hostPath string, ownerUID uint32) string {
	return fmt.Sprintf(
		"host_path %s is owned by uid=%d, but the mind-form container runs as uid=%d. "+
			"Writes from the mind-form may fail with EACCES. "+
			"Run 'chown -R %d %s' to align, or pass --no-warn-uid to suppress.",
		hostPath, ownerUID, MindFormContainerUID, MindFormContainerUID, hostPath,
	)
}
