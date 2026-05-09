package relay

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsEventStoreLocked(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "unrelated", err: errors.New("disk full"), want: false},
		{
			name: "badger lock prefix",
			err:  errors.New(`Cannot acquire directory lock on "/path".  Another process is using this Badger database. err: resource temporarily unavailable`),
			want: true,
		},
		{
			// Mirror the substring path even when Badger's prefix is
			// rephrased — `resource temporarily unavailable` is the
			// underlying syscall.EAGAIN string and is a strong signal on
			// its own.
			name: "ewouldblock only",
			err:  fmt.Errorf("flock: %w", errors.New("resource temporarily unavailable")),
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isEventStoreLocked(c.err)
			if got != c.want {
				t.Errorf("isEventStoreLocked(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
