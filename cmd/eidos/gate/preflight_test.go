package gate

import (
	"testing"
)

func TestPortFromHostPort(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:22895": "22895",
		"0.0.0.0:8080":    "8080",
		"[::1]:9000":      "9000",
		"not-a-host-port": "not-a-host-port",
		"":                "",
	}
	for in, want := range cases {
		if got := portFromHostPort(in); got != want {
			t.Errorf("portFromHostPort(%q) = %q, want %q", in, got, want)
		}
	}
}
