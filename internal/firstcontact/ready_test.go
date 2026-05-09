package firstcontact_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact"
)

func TestStartBackground_AllReady(t *testing.T) {
	deps := firstcontact.ReadyDeps{
		PullImage:    func(_ context.Context) error { return nil },
		GenerateKey:  func() (string, string, error) { return "npub1x", "hex", nil },
		ProbeRelay:   func(_ context.Context, _ string) error { return nil },
		HomeRelayURL: "wss://r/",
	}
	ch := firstcontact.StartBackground(context.Background(), deps)
	final := drainReady(t, ch, 500*time.Millisecond)
	if !final.AllReady() {
		t.Errorf("not all ready: %+v", final)
	}
	if final.MindFormNpub != "npub1x" || final.MindFormKeyHex != "hex" {
		t.Errorf("keypair fields not propagated: %+v", final)
	}
}

func TestStartBackground_FailureSurfaces(t *testing.T) {
	deps := firstcontact.ReadyDeps{
		PullImage:    func(_ context.Context) error { return errors.New("docker daemon down") },
		GenerateKey:  func() (string, string, error) { return "n", "h", nil },
		ProbeRelay:   func(_ context.Context, _ string) error { return nil },
		HomeRelayURL: "wss://r/",
	}
	ch := firstcontact.StartBackground(context.Background(), deps)
	final := drainReady(t, ch, 500*time.Millisecond)
	if final.DockerImage.Status != "failed" {
		t.Errorf("docker status = %q, want failed", final.DockerImage.Status)
	}
	if final.DockerImage.Error == "" {
		t.Errorf("docker error not surfaced")
	}
	if !final.AnyFailed() {
		t.Errorf("AnyFailed = false despite failure: %+v", final)
	}
}

func drainReady(t *testing.T, ch <-chan firstcontact.ReadyState, deadline time.Duration) firstcontact.ReadyState {
	t.Helper()
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	var last firstcontact.ReadyState
	for {
		select {
		case s, ok := <-ch:
			if !ok {
				return last
			}
			last = s
		case <-timer.C:
			return last
		}
	}
}
