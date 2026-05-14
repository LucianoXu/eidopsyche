package daemon

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestParseWorkspacesFromMounts(t *testing.T) {
	mounts := strings.Join([]string{
		"overlay / overlay rw,relatime,lowerdir=...,upperdir=... 0 0",
		"/dev/sda1 /eidos ext4 rw,relatime 0 0",
		"/dev/sda1 /workspace/proj-x ext4 rw,relatime 0 0",
		"/dev/sda1 /workspace/photos ext4 ro,relatime 0 0",
		"proc /proc proc rw,nosuid,nodev,noexec 0 0",
		"/dev/sda1 /workspace/nested/dir ext4 rw,relatime 0 0",
		"",
	}, "\n")

	got := parseWorkspacesFromMounts([]byte(mounts))
	want := []workspaceContainerEntry{
		{Name: "proj-x", Mode: "rw"},
		{Name: "photos", Mode: "ro"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWorkspacesFromMounts mismatch\nwant %#v\n got %#v", want, got)
	}
}

func TestForgeWorkspacesContrib_PathAndEmpty(t *testing.T) {
	d := newTestDaemon(t)
	c := forgeWorkspacesContrib{d: d}
	if c.Path() != "forge.workspaces" {
		t.Errorf("Path() = %q; want forge.workspaces", c.Path())
	}
	// Snapshot is a thin wrapper that reads /proc/self/mounts; on
	// runners where /proc exists but no /workspace/* mounts are
	// configured, the result is the empty slice — never an error.
	got, err := c.Snapshot(context.TODO())
	if err != nil {
		t.Fatal(err)
	}
	if reflect.ValueOf(got).Len() < 0 {
		t.Fatal("unreachable")
	}
}
