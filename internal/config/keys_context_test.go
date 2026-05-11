package config

import "testing"

// TestContextBitmask_HeartbeatIsContainerOnly guards the design
// invariant that heartbeat.interval is only settable from inside a
// mindform container. The upcoming Mutate framework relies on this to
// reject `eidos gate config set heartbeat.interval 1m` on host with a
// helpful CONTEXT_MISMATCH error.
func TestContextBitmask_HeartbeatIsContainerOnly(t *testing.T) {
	k, ok := KeyByPath("heartbeat.interval")
	if !ok {
		t.Fatal("heartbeat.interval not registered")
	}
	if k.Contexts == 0 {
		t.Errorf("heartbeat.interval.Contexts must be nonzero; got %d", k.Contexts)
	}
	if k.Contexts&ContainerCtx == 0 {
		t.Errorf("heartbeat.interval must be valid in ContainerCtx; got %d", k.Contexts)
	}
	if k.Contexts&HostCtx != 0 {
		t.Errorf("heartbeat.interval must NOT be valid in HostCtx; got %d", k.Contexts)
	}
}

func TestContextBitmask_ModelIsContainerOnly(t *testing.T) {
	k, ok := KeyByPath("mindform.model")
	if !ok {
		t.Fatal("mindform.model not registered")
	}
	if k.Contexts&HostCtx != 0 {
		t.Errorf("mindform.model must not have HostCtx; got %d", k.Contexts)
	}
	if k.Contexts&ContainerCtx == 0 {
		t.Errorf("mindform.model must have ContainerCtx; got %d", k.Contexts)
	}
}

func TestContextBitmask_LogLevelIsBoth(t *testing.T) {
	k, ok := KeyByPath("log_level")
	if !ok {
		t.Fatal("log_level not registered")
	}
	if k.Contexts != BothCtx {
		t.Errorf("log_level.Contexts should be BothCtx; got %d", k.Contexts)
	}
}

func TestContextBitmask_AllKeysHaveContexts(t *testing.T) {
	for _, k := range KeyList() {
		if k.Contexts == 0 {
			t.Errorf("key %s has zero Contexts — every registered key must specify at least one context", k.Path)
		}
	}
}
