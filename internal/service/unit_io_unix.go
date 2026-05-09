//go:build linux || darwin

package service

import "os"

// writeUnitIfChanged writes content to path only when it differs from the
// existing file. It's only used by the systemd/linux and launchd/darwin
// Manager backends, both of which materialize their service definition as a
// unit file on disk. The Windows SCM backend stores config inside SCM, so
// this helper is unix-only.
//
// Avoiding gratuitous rewrites keeps mtimes stable, which matters because:
//   - systemd would otherwise treat a same-content rewrite as a reload
//     trigger via `daemon-reload` heuristics, and
//   - launchd needs an explicit `bootout` + `bootstrap` cycle to pick up
//     plist changes; we'd rather skip that round-trip when nothing changed.
func writeUnitIfChanged(path, content string) error {
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == content {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
