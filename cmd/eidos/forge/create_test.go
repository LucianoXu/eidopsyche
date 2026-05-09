package forge

import (
	"strings"
	"testing"
)

func TestCreateFlagValidation(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing all flags",
			args:    []string{"create", "alice"},
			wantErr: "owner",
		},
		{
			name:    "missing relay",
			args:    []string{"create", "alice", "--owner", "npub1ownertest"},
			wantErr: "relay",
		},
		{
			name:    "missing name",
			args:    []string{"create", "--owner", "npub1x", "--relay", "wss://x"},
			wantErr: "name",
		},
		{
			name:    "invalid name",
			args:    []string{"create", "Alice", "--owner", "npub1x", "--relay", "wss://x"},
			wantErr: "invalid mind-form name",
		},
		{
			name:    "invalid owner",
			args:    []string{"create", "alice", "--owner", "not-an-npub", "--relay", "wss://x"},
			wantErr: "owner",
		},
		{
			name:    "invalid relay scheme",
			args:    []string{"create", "alice", "--owner", "npub1ownertest", "--relay", "http://x"},
			wantErr: "relay must be ws:// or wss://",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := Command()
			cmd.SetArgs(c.args)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("want error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error = %q; want substring %q", err.Error(), c.wantErr)
			}
		})
	}
}
