package config

import "testing"

func TestValidateModelID(t *testing.T) {
	cases := []struct {
		in   string
		want bool // true = should be accepted
	}{
		{"", true}, // empty = use claude default
		{"claude-sonnet-4-7", true},
		{"claude-haiku-4-5", true},
		{"claude-opus-4-7", true},
		{"claude-sonnet-4-6-20250101", true},
		// Reject: wrong family
		{"claude-foo-4-7", false},
		// Reject: missing version
		{"claude-sonnet", false},
		// Reject: not claude-prefixed
		{"sonnet-4-7", false},
		// Reject: non-numeric segment
		{"claude-sonnet-x-7", false},
		// Reject: trailing junk (would be a shell-injection vector)
		{"claude-sonnet-4-7 ; rm -rf /", false},
	}
	for _, c := range cases {
		err := ValidateModelID(c.in)
		got := err == nil
		if got != c.want {
			t.Errorf("ValidateModelID(%q): got accepted=%v err=%v, want accepted=%v",
				c.in, got, err, c.want)
		}
	}
}
