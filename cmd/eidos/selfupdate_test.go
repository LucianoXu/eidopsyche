package main

import (
	"slices"
	"strings"
	"testing"
)

// TestSelfUpdateChildEnv_NoRestartFalsePassesParent confirms that the
// default behavior leaves the parent environment untouched, so a user
// who exported their own EIDOS_NO_RESTART=1 still sees the install
// script honour it.
func TestSelfUpdateChildEnv_NoRestartFalsePassesParent(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "FOO=bar", "EIDOS_NO_RESTART=1"}
	got := selfUpdateChildEnv(parent, false)
	if !equalSliceUnordered(got, parent) {
		t.Errorf("--no-restart=false should pass env through; got %v, want %v", got, parent)
	}
}

// TestSelfUpdateChildEnv_NoRestartTrueAddsFlag confirms --no-restart sets
// EIDOS_NO_RESTART=1 even when the parent did not have it.
func TestSelfUpdateChildEnv_NoRestartTrueAddsFlag(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "FOO=bar"}
	got := selfUpdateChildEnv(parent, true)
	if !contains(got, "EIDOS_NO_RESTART=1") {
		t.Errorf("expected EIDOS_NO_RESTART=1 in env; got %v", got)
	}
}

// TestSelfUpdateChildEnv_NoRestartTrueOverridesExisting confirms a stray
// EIDOS_NO_RESTART=0 in the parent gets replaced rather than appended,
// so the install script reads exactly one canonical value.
func TestSelfUpdateChildEnv_NoRestartTrueOverridesExisting(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "EIDOS_NO_RESTART=0", "FOO=bar"}
	got := selfUpdateChildEnv(parent, true)

	count := 0
	for _, kv := range got {
		if strings.HasPrefix(kv, "EIDOS_NO_RESTART=") {
			count++
			if kv != "EIDOS_NO_RESTART=1" {
				t.Errorf("EIDOS_NO_RESTART preserved as %q; want EIDOS_NO_RESTART=1", kv)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one EIDOS_NO_RESTART entry, got %d in %v", count, got)
	}
	// Other parent vars must survive.
	if !contains(got, "PATH=/usr/bin") || !contains(got, "FOO=bar") {
		t.Errorf("unrelated env vars dropped: %v", got)
	}
}

// TestSelfUpdateChildEnv_DoesNotMatchPrefixed confirms a similarly-named
// variable like EIDOS_NO_RESTART_HINT (hypothetical, but the matcher
// must be exact-equality, not prefix) is not stripped.
func TestSelfUpdateChildEnv_DoesNotMatchPrefixed(t *testing.T) {
	parent := []string{"EIDOS_NO_RESTART_HINT=foo"}
	got := selfUpdateChildEnv(parent, true)
	if !contains(got, "EIDOS_NO_RESTART_HINT=foo") {
		t.Errorf("similarly-named var was incorrectly stripped: %v", got)
	}
}

func contains(s []string, want string) bool {
	return slices.Contains(s, want)
}

func equalSliceUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	count := map[string]int{}
	for _, v := range a {
		count[v]++
	}
	for _, v := range b {
		count[v]--
	}
	for _, n := range count {
		if n != 0 {
			return false
		}
	}
	return true
}
