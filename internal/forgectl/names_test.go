package forgectl

import "testing"

func TestValidateName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"alice", true},
		{"al", true},
		{"a-very-long-but-still-valid", true},
		{"a", false},         // too short
		{"Alice", false},     // uppercase
		{"1alice", false},    // leading digit
		{"alice-", false},    // trailing hyphen
		{"alice_bob", false}, // underscore
		{"", false},
	}
	for _, c := range cases {
		err := ValidateName(c.in)
		if (err == nil) != c.want {
			t.Errorf("ValidateName(%q): err=%v, want ok=%v", c.in, err, c.want)
		}
	}
}

func TestNamePrefixes(t *testing.T) {
	if VolumeName("alice") != "eidos-mindform-alice" {
		t.Errorf("volume name wrong: %q", VolumeName("alice"))
	}
	if ContainerName("alice") != "eidos-mindform-alice" {
		t.Errorf("container name wrong: %q", ContainerName("alice"))
	}
}
