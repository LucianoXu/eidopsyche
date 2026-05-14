// Package claudeexec — tools.go: framework-level allowlist of the
// Claude Code built-in tools the mind-form is permitted to invoke.
//
// docs/specs/SPEC.md §"Claude Code 调用设计" is the prose authority;
// this slice is its executable form. Edit them together.
//
// Allowlist names target Claude Code 2.1.141 naming. The runtime
// image (docker/mindform/Dockerfile's CLAUDE_CODE_VERSION) may pin
// an older patch; every name in this list is still valid in the
// currently shipped CC patch (claude logs and ignores unknown names
// rather than failing). When bumping CLAUDE_CODE_VERSION, re-validate
// this list against the upstream release notes — tools added /
// removed / renamed upstream must be reflected here and in SPEC.md.
package claudeexec

import "strings"

// Allowed is the canonical list of Claude Code built-in tool names
// the mind-form's claude subprocess is permitted to load. Order is
// stable for diffability of rendered argv and golden tests.
var Allowed = []string{
	// file ops
	"Read", "Write", "Edit", "Glob", "Grep",
	// execution
	"Bash",
	// sub-agents and pacing
	"Agent", "ScheduleWakeup", "Monitor",
	// background task management
	"TaskOutput", "TaskStop",
	// turn-local todo list
	"TodoWrite",
	// long-term method invocation
	"Skill",
	// outward exploration
	"WebFetch", "WebSearch",
}

// ToolsArg returns the comma-separated value to pass to claude's
// --tools flag. Stable order, no spaces.
func ToolsArg() string {
	return strings.Join(Allowed, ",")
}
