package claudeexec

import (
	"strings"
	"testing"
)

func TestAllowed_NonEmpty(t *testing.T) {
	if len(Allowed) == 0 {
		t.Fatal("claudeexec.Allowed must not be empty (would render --tools \"\" → disable all tools)")
	}
}

func TestAllowed_NoDuplicates(t *testing.T) {
	seen := make(map[string]struct{}, len(Allowed))
	for _, name := range Allowed {
		if _, dup := seen[name]; dup {
			t.Errorf("duplicate tool name %q in Allowed", name)
		}
		seen[name] = struct{}{}
	}
}

func TestToolsArg_GoldenString(t *testing.T) {
	// Golden value mirrors the SPEC.md §"Claude Code 调用设计" table.
	// If you change either side, change them together — the diff for
	// this line is the review gate.
	const want = "Read,Write,Edit,Glob,Grep,Bash,Agent,ScheduleWakeup,Monitor,TaskOutput,TaskStop,TodoWrite,Skill,WebFetch,WebSearch"
	if got := ToolsArg(); got != want {
		t.Errorf("ToolsArg mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestToolsArg_NoWhitespace(t *testing.T) {
	if strings.ContainsAny(ToolsArg(), " \t\n") {
		t.Errorf("ToolsArg must be whitespace-free, got %q", ToolsArg())
	}
}
