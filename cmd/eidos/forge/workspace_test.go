package forge

import "testing"

func TestWorkspaceCmd_SubcommandsRegistered(t *testing.T) {
	cmd := newWorkspaceCmd()
	want := []string{"add", "remove", "list"}
	have := map[string]bool{}
	for _, sub := range cmd.Commands() {
		have[sub.Name()] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("missing subcommand: %s", w)
		}
	}
}
