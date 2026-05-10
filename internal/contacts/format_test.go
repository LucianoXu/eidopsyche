package contacts

import "testing"

func TestFormatPubkey(t *testing.T) {
	const hex64 = "abc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abc"

	cases := []struct {
		name   string
		label  string
		pubkey string
		want   string
	}{
		{"label wins over hex", "alice", hex64, "alice"},
		{"label wins over empty pubkey", "alice", "", "alice"},
		{"empty label falls back to short hex", "", hex64, "abc1234…"},
		{"both empty yields empty", "", "", ""},
		{"shorter-than-prefix pubkey returned verbatim", "", "abc12", "abc12"},
		{"exact-prefix pubkey returned verbatim", "", "abc1234", "abc1234"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatPubkey(tc.label, tc.pubkey); got != tc.want {
				t.Errorf("FormatPubkey(%q, %q) = %q, want %q", tc.label, tc.pubkey, got, tc.want)
			}
		})
	}
}

func TestShortHex(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"abc", "abc"},
		{"abc1234", "abc1234"},
		{"abc12345", "abc1234…"},
		{"abc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abc", "abc1234…"},
	}
	for _, tc := range cases {
		if got := ShortHex(tc.in); got != tc.want {
			t.Errorf("ShortHex(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatPubkeyWithHex(t *testing.T) {
	const hex = "abc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abc"
	cases := []struct {
		name   string
		label  string
		pubkey string
		want   string
	}{
		{"label and pubkey", "alice", hex, "alice (abc1234…)"},
		{"unknown sender", "", hex, "abc1234…"},
		{"empty pubkey with label", "alice", "", "alice ()"},
		{"both empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatPubkeyWithHex(tc.label, tc.pubkey); got != tc.want {
				t.Errorf("FormatPubkeyWithHex(%q, %q) = %q, want %q", tc.label, tc.pubkey, got, tc.want)
			}
		})
	}
}
