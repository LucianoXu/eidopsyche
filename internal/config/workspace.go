package config

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
