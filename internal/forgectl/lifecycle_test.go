package forgectl

import (
	"context"
	"errors"
	"io"
	"testing"
)

// minimalFake is a small Client fake used only by lifecycle_test. The
// richer fake in cmd/eidos/forge/create_test.go is intentionally kept
// internal to that test file (orchestration tests need it more); this
// fake is the minimal Client surface needed to exercise
// PurgeForFailedSummon.
type minimalFake struct {
	hasContainer  bool
	hasVolume     bool
	rmContainerN  int
	rmVolumeN     int
	rmContainerEr error
}

func (f *minimalFake) VolumeExists(_ context.Context, _ string) (bool, error) {
	return f.hasVolume, nil
}
func (f *minimalFake) VolumeCreate(_ context.Context, _ string) error { return nil }
func (f *minimalFake) VolumeRemove(_ context.Context, _ string) error {
	f.rmVolumeN++
	f.hasVolume = false
	return nil
}
func (f *minimalFake) ContainerExists(_ context.Context, _ string) (bool, error) {
	return f.hasContainer, nil
}
func (f *minimalFake) ContainerInspectState(_ context.Context, _ string) (string, error) {
	return "absent", nil
}
func (f *minimalFake) ContainerCreate(_ context.Context, _ CreateOpts) error { return nil }
func (f *minimalFake) ContainerStart(_ context.Context, _ string) error      { return nil }
func (f *minimalFake) ContainerStop(_ context.Context, _ string, _ int) error {
	return nil
}
func (f *minimalFake) ContainerRemove(_ context.Context, _ string) error {
	f.rmContainerN++
	if f.rmContainerEr != nil {
		return f.rmContainerEr
	}
	f.hasContainer = false
	return nil
}
func (f *minimalFake) ImageExists(_ context.Context, _ string) (bool, error)     { return true, nil }
func (f *minimalFake) ImagePull(_ context.Context, _ string, _ io.Writer) error  { return nil }
func (f *minimalFake) RunInit(_ context.Context, _ RunInitOpts) (RunInitResult, error) {
	return RunInitResult{}, nil
}
func (f *minimalFake) ContainerExec(_ context.Context, _ string, _ []string) (ExecResult, error) {
	return ExecResult{}, nil
}
func (f *minimalFake) VolumeList(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (f *minimalFake) ContainerLogs(_ context.Context, _ string, _ bool, _ io.Writer) error {
	return nil
}
func (f *minimalFake) CopyFromContainer(_ context.Context, _, _ string, _ io.Writer) error {
	return nil
}

func TestPurgeForFailedSummon_RemovesBoth(t *testing.T) {
	f := &minimalFake{hasContainer: true, hasVolume: true}
	if err := PurgeForFailedSummon(context.Background(), f, "yu"); err != nil {
		t.Fatalf("PurgeForFailedSummon: %v", err)
	}
	if f.rmContainerN != 1 {
		t.Errorf("ContainerRemove called %d times, want 1", f.rmContainerN)
	}
	if f.rmVolumeN != 1 {
		t.Errorf("VolumeRemove called %d times, want 1", f.rmVolumeN)
	}
}

func TestPurgeForFailedSummon_TolerantOfMissing(t *testing.T) {
	f := &minimalFake{hasContainer: false, hasVolume: false}
	if err := PurgeForFailedSummon(context.Background(), f, "yu"); err != nil {
		t.Errorf("PurgeForFailedSummon on empty fake: %v", err)
	}
	if f.rmContainerN != 0 || f.rmVolumeN != 0 {
		t.Errorf("removes called when nothing existed: cont=%d vol=%d", f.rmContainerN, f.rmVolumeN)
	}
}

func TestPurgeForFailedSummon_StillRemovesVolumeAfterContainerError(t *testing.T) {
	f := &minimalFake{
		hasContainer:  true,
		hasVolume:     true,
		rmContainerEr: errors.New("docker is on fire"),
	}
	err := PurgeForFailedSummon(context.Background(), f, "yu")
	if err == nil {
		t.Fatalf("expected error from container-remove")
	}
	if f.rmVolumeN != 1 {
		t.Errorf("VolumeRemove not attempted after container-remove failure: %d", f.rmVolumeN)
	}
}
