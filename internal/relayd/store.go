package relayd

import (
	"fmt"

	"github.com/fiatjaf/eventstore/badger"
)

// OpenEventStore creates and initializes a badger-backed eventstore at
// dir (a directory; badger creates SST files inside). The caller owns
// Close().
func OpenEventStore(dir string) (*badger.BadgerBackend, error) {
	if dir == "" {
		return nil, fmt.Errorf("OpenEventStore: dir is empty")
	}
	b := &badger.BadgerBackend{Path: dir}
	if err := b.Init(); err != nil {
		return nil, fmt.Errorf("eventstore badger init %s: %w", dir, err)
	}
	return b, nil
}
