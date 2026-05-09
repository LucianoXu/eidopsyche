package relayd

import (
	"fmt"

	"github.com/fiatjaf/eventstore/sqlite3"
)

// OpenSQLiteStore creates and initializes a sqlite-backed eventstore at
// path. The caller owns Close().
func OpenSQLiteStore(path string) (*sqlite3.SQLite3Backend, error) {
	if path == "" {
		return nil, fmt.Errorf("OpenSQLiteStore: path is empty")
	}
	b := &sqlite3.SQLite3Backend{DatabaseURL: path}
	if err := b.Init(); err != nil {
		return nil, fmt.Errorf("eventstore sqlite3 init %s: %w", path, err)
	}
	return b, nil
}
