package config

import "testing"

func TestValidateEffort(t *testing.T) {
	cases := []struct {
		in   string
		want bool // true = should be accepted
	}{
		{"", true},      // empty = use DefaultEffort
		{"low", true},
		{"medium", true},
		{"high", true},
		// Reject: unknown level
		{"ultra", false},
		// Reject: wrong case
		{"Low", false},
		{"MEDIUM", false},
		// Reject: trailing junk
		{"low; rm -rf /", false},
	}
	for _, c := range cases {
		err := ValidateEffort(c.in)
		got := err == nil
		if got != c.want {
			t.Errorf("ValidateEffort(%q): got accepted=%v err=%v, want accepted=%v",
				c.in, got, err, c.want)
		}
	}
}
