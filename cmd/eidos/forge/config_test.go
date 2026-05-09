package forge

import (
	"strings"
	"testing"
)

func TestConfigArgv_Model(t *testing.T) {
	argv, err := buildConfigDockerArgv("alice", configFlags{model: "claude-sonnet-4-7"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "eidos-mindform-alice", "eidos", "gate", "config", "set", "mindform.model", "claude-sonnet-4-7"}
	if !equalArgv(argv, want) {
		t.Errorf("argv = %v, want %v", argv, want)
	}
}

func TestConfigArgv_BadModel(t *testing.T) {
	_, err := buildConfigDockerArgv("alice", configFlags{model: "garbage"})
	if err == nil {
		t.Fatal("garbage model should be rejected at host before we exec")
	}
	if !strings.Contains(err.Error(), "model") {
		t.Errorf("err should mention model, got: %v", err)
	}
}

func TestConfigArgv_NoFlag(t *testing.T) {
	_, err := buildConfigDockerArgv("alice", configFlags{})
	if err == nil {
		t.Error("forge config without --model should error")
	}
}

func equalArgv(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
