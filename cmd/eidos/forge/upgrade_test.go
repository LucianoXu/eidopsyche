package forge

import (
	"bytes"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// fakeIPC is a test-side stand-in for the CLI's IPC client.
type fakeIPC struct {
	calls   []ipc.ForgeUpgradeParams
	results []ipc.ForgeUpgradeResult
	errs    []*ipc.Error
}

func (f *fakeIPC) Call(method string, params ipc.ForgeUpgradeParams) (ipc.ForgeUpgradeResult, *ipc.Error) {
	idx := len(f.calls)
	f.calls = append(f.calls, params)
	if idx < len(f.errs) && f.errs[idx] != nil {
		return ipc.ForgeUpgradeResult{}, f.errs[idx]
	}
	if idx < len(f.results) {
		return f.results[idx], nil
	}
	return ipc.ForgeUpgradeResult{}, nil
}

func TestUpgrade_RendersDiff(t *testing.T) {
	fake := &fakeIPC{
		results: []ipc.ForgeUpgradeResult{
			{
				Name:          "alice",
				OldImage:      "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2",
				NewImage:      "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3",
				OldEidos:      "v0.11.2",
				NewEidos:      "v0.11.3",
				OldClaudeCode: "2.1.138",
				NewClaudeCode: "2.1.140",
				DryRun:        true,
			},
		},
	}
	var out bytes.Buffer
	err := runUpgradeWithIPC(&out, fake, "alice", upgradeFlags{DryRun: true, NonTTY: true})
	if err != nil {
		t.Fatalf("runUpgradeWithIPC: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"upgrading alice:",
		"v0.11.2",
		"v0.11.3",
		"2.1.138",
		"2.1.140",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n---\n%s\n---", want, got)
		}
	}
	if len(fake.calls) != 1 {
		t.Errorf("dry-run flag should make exactly one IPC call, got %d", len(fake.calls))
	}
}

func TestUpgrade_SkippedExits(t *testing.T) {
	fake := &fakeIPC{
		results: []ipc.ForgeUpgradeResult{
			{
				Name:          "alice",
				OldImage:      "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2",
				NewImage:      "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2",
				Skipped:       true,
				SkippedReason: "already at v0.11.2",
				DryRun:        true,
			},
		},
	}
	var out bytes.Buffer
	err := runUpgradeWithIPC(&out, fake, "alice", upgradeFlags{NonTTY: true})
	if err != nil {
		t.Fatalf("runUpgradeWithIPC: %v", err)
	}
	if !strings.Contains(out.String(), "already at v0.11.2") {
		t.Errorf("expected skipped message; got: %s", out.String())
	}
	if len(fake.calls) != 1 {
		t.Errorf("skipped path should make exactly one IPC call, got %d", len(fake.calls))
	}
}

func TestUpgrade_NonTTYAutoConfirmsRealRun(t *testing.T) {
	fake := &fakeIPC{
		results: []ipc.ForgeUpgradeResult{
			{ // DryRun preview
				Name: "alice", OldEidos: "v0.11.2", NewEidos: "v0.11.3", DryRun: true,
				OldImage: "x", NewImage: "y",
			},
			{ // Live run
				Name: "alice", OldEidos: "v0.11.2", NewEidos: "v0.11.3",
				OldImage: "x", NewImage: "y",
			},
		},
	}
	var out bytes.Buffer
	err := runUpgradeWithIPC(&out, fake, "alice", upgradeFlags{NonTTY: true})
	if err != nil {
		t.Fatalf("runUpgradeWithIPC: %v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("non-TTY should make two IPC calls (preview + live), got %d", len(fake.calls))
	}
	if fake.calls[0].DryRun != true {
		t.Errorf("first call must be DryRun=true")
	}
	if fake.calls[1].DryRun != false {
		t.Errorf("second call must be DryRun=false")
	}
}
