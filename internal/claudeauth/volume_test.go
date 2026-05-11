package claudeauth

import (
	"strings"
	"testing"
)

type fakeVolumeWriter struct {
	writes []struct {
		relPath string
		body    string
	}
	cleared bool
	err     error
}

func (f *fakeVolumeWriter) Write(relPath string, body []byte) error {
	if f.err != nil {
		return f.err
	}
	f.writes = append(f.writes, struct {
		relPath string
		body    string
	}{relPath, string(body)})
	return nil
}

func (f *fakeVolumeWriter) ClearAuthRequired() error {
	f.cleared = true
	return nil
}

func TestWriteToVolume_HappyPath(t *testing.T) {
	blob := []byte(`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1}}`)
	w := &fakeVolumeWriter{}
	if err := writeToVolumeUsing(w, blob); err != nil {
		t.Fatalf("writeToVolumeUsing: %v", err)
	}
	if len(w.writes) != 1 {
		t.Fatalf("want 1 write, got %d", len(w.writes))
	}
	if w.writes[0].relPath != "claude/.claude/.credentials.json" {
		t.Errorf("relPath = %q, want claude/.claude/.credentials.json", w.writes[0].relPath)
	}
	if !w.cleared {
		t.Errorf("auth_required not cleared after successful write")
	}
}

func TestWriteToVolume_RejectsInvalidBlob(t *testing.T) {
	blob := []byte(`{}`)
	w := &fakeVolumeWriter{}
	err := writeToVolumeUsing(w, blob)
	if err == nil || !strings.Contains(err.Error(), "claudeAiOauth") {
		t.Fatalf("want validate error, got %v", err)
	}
	if len(w.writes) != 0 {
		t.Errorf("write should not happen on invalid blob, got %d writes", len(w.writes))
	}
}
