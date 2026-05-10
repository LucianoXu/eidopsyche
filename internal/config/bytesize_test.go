package config

import "testing"

func TestParseByteSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		err  bool
	}{
		{"100", 100, false},
		{"100B", 100, false},
		{"1K", 1024, false},
		{"1KB", 1024, false},
		{"1KiB", 1024, false},
		{"100M", 100 * 1024 * 1024, false},
		{"100MB", 100 * 1024 * 1024, false},
		{"5G", 5 * 1024 * 1024 * 1024, false},
		{"5GB", 5 * 1024 * 1024 * 1024, false},
		{"  10  ", 10, false},
		{"100mb", 100 * 1024 * 1024, false}, // case-insensitive
		{"", 0, true},
		{"abc", 0, true},
		{"-1", 0, true},
		{"-1K", 0, true},
	}
	for _, c := range cases {
		got, err := ParseByteSize(c.in)
		if c.err {
			if err == nil {
				t.Errorf("ParseByteSize(%q): expected error, got %d", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseByteSize(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseByteSize(%q): got %d, want %d", c.in, got, c.want)
		}
	}
}
