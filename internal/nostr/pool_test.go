package nostr

import "testing"

func TestPoolConstruct(t *testing.T) {
	p := NewPool()
	if p == nil {
		t.Fatal("nil pool")
	}
	p.Close()
}
