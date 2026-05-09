package forge

import (
	"strings"
	"testing"
)

func TestConfigArgv_Model(t *testing.T) {
	argv, err := buildConfigDockerArgv("alice", configFlags{model: "claude-sonnet-4-7", modelSet: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "eidos-mindform-alice", "eidos", "gate", "config", "set", "mindform.model", "claude-sonnet-4-7"}
	if !equalArgv(argv, want) {
		t.Errorf("argv = %v, want %v", argv, want)
	}
}

func TestConfigArgv_Unpin(t *testing.T) {
	// Empty --model with modelSet=true is the "unpin" path: it must
	// produce a valid argv so the in-container setter writes "" through.
	argv, err := buildConfigDockerArgv("alice", configFlags{model: "", modelSet: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "eidos-mindform-alice", "eidos", "gate", "config", "set", "mindform.model", ""}
	if !equalArgv(argv, want) {
		t.Errorf("argv = %v, want %v", argv, want)
	}
}

func TestConfigArgv_BadModel(t *testing.T) {
	_, err := buildConfigDockerArgv("alice", configFlags{model: "garbage", modelSet: true})
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
		t.Error("forge config without any flag should error")
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
