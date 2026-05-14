package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

func TestForgeUpgrade_Registered(t *testing.T) {
	if _, ok := methodTable["forge.upgrade"]; !ok {
		t.Fatal("forge.upgrade is not registered in methodTable")
	}
}

func TestForgeUpgrade_EmptyName(t *testing.T) {
	fn, ok := methodTable["forge.upgrade"]
	if !ok {
		t.Fatal("forge.upgrade not registered")
	}
	params, _ := json.Marshal(ipc.ForgeUpgradeParams{Name: ""})
	_, ierr := fn(context.Background(), nil, nil, params)
	if ierr == nil || ierr.Code != ipc.ErrInvalidParams {
		t.Fatalf("expected ErrInvalidParams for empty name, got %v", ierr)
	}
}

func TestForgeUpgrade_BadIdleTimeout(t *testing.T) {
	fn := methodTable["forge.upgrade"]
	params, _ := json.Marshal(ipc.ForgeUpgradeParams{Name: "alice", IdleTimeout: "not-a-duration"})
	_, ierr := fn(context.Background(), nil, nil, params)
	if ierr == nil || ierr.Code != ipc.ErrInvalidParams {
		t.Fatalf("expected ErrInvalidParams for malformed idle_timeout, got %v", ierr)
	}
	if !strings.Contains(strings.ToLower(ierr.Message), "idle_timeout") {
		t.Errorf("error message should mention the bad field; got: %s", ierr.Message)
	}
}

func TestForgeUpgrade_BadName(t *testing.T) {
	fn := methodTable["forge.upgrade"]
	// Name with characters forgectl.ValidateName rejects (must start with letter,
	// rest letters/digits/hyphens; see internal/forgectl/names.go for the rule).
	params, _ := json.Marshal(ipc.ForgeUpgradeParams{Name: "with spaces"})
	_, ierr := fn(context.Background(), nil, nil, params)
	if ierr == nil || ierr.Code != ipc.ErrInvalidParams {
		t.Fatalf("expected ErrInvalidParams for invalid name characters, got %v", ierr)
	}
}
