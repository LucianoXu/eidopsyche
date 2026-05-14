package inbox

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Store struct {
	root string
	mu   sync.Mutex
}

func New(root string) *Store { return &Store{root: root} }

func (s *Store) inboxDir() string  { return filepath.Join(s.root, "inbox") }
func (s *Store) outboxDir() string { return filepath.Join(s.root, "outbox") }

func dailyPath(base string, ts time.Time) string {
	t := ts.UTC()
	return filepath.Join(base,
		fmt.Sprintf("%04d", t.Year()),
		fmt.Sprintf("%02d", t.Month()),
		fmt.Sprintf("%02d.jsonl", t.Day()))
}

func (s *Store) AppendInbox(m Message) error {
	if m.V == 0 {
		m.V = 1
	}
	if m.ReceivedAt == 0 {
		m.ReceivedAt = time.Now().Unix()
	}
	// Transit-only annotations: defense against accidental persistence.
	// Both fields are reconstructed at read time from the contacts repo
	// (label) and the IsPending predicate (pending). Persisting them
	// would freeze the classification at write time, defeating the
	// promote-and-the-pending-row-becomes-known behaviour.
	m.Label = ""
	m.Pending = false
	return s.appendJSONL(dailyPath(s.inboxDir(), time.Unix(m.ReceivedAt, 0)), m)
}

func (s *Store) appendJSONL(path string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return f.Sync()
}

// ListInbox returns inbox messages newest-first up to limit.
//
// Rows are collapsed by EventID so duplicates persisted across daemon
// restarts (see Daemon.dedupe / hydrate rationale) appear once. The
// scan stays streaming newest-first and short-circuits as soon as
// limit collapsed rows have been emitted, so a small dashboard page
// load still costs O(limit) rather than O(total inbox history).
//
// Newest-wins semantics: when the same EventID is seen in older files,
// later occurrences are dropped. The bubble timestamp reflects the
// most recent ingestion (typically the post-restart replay) and the
// surviving Malformed / RejectReason fields reflect the current
// decoder's verdict — a clean replay supersedes a stale soft-reject.
//
// An optional keep predicate (variadic, at most one accepted) is invoked
// per surviving row; rows for which keep returns false are skipped and
// do not count against limit. This is how the daemon applies the
// `sender=known|unknown|all` filter on inbox.list without losing the
// "exactly limit matching rows" page-size semantic. keep omitted or nil
// keeps every row.
func (s *Store) ListInbox(since *time.Time, from string, limit int, keep ...func(Message) bool) ([]Message, error) {
	var keepFn func(Message) bool
	if len(keep) > 0 {
		keepFn = keep[0]
	}
	files, err := s.daysDescending(s.inboxDir())
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	out := make([]Message, 0, limit)
	for _, f := range files {
		msgs, err := readInboxFile(f)
		if err != nil {
			return nil, err
		}
		for i := len(msgs) - 1; i >= 0; i-- {
			m := msgs[i]
			if since != nil && time.Unix(m.ReceivedAt, 0).Before(*since) {
				return out, nil
			}
			if from != "" && m.From != from {
				continue
			}
			if m.EventID != "" {
				if _, dup := seen[m.EventID]; dup {
					continue
				}
				seen[m.EventID] = struct{}{}
			}
			if keepFn != nil && !keepFn(m) {
				continue
			}
			out = append(out, m)
			if limit > 0 && len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

func readInboxFile(path string) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Message
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var m Message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		out = append(out, m)
	}
	return out, sc.Err()
}

// EventIDs returns the set of distinct inbox EventIDs persisted on disk.
//
// Used by the daemon at startup to rebuild its in-memory dedupe map so a
// relay re-delivering an event after daemon restart doesn't append a
// duplicate inbox row. Without this, every restart bloats inbox.jsonl
// with replays the relay sends as part of its since=<lastseen> backlog.
//
// Memory is bounded by the active event population — a 32-byte event id
// per row, typically well under 1 MB even for years of personal usage.
func (s *Store) EventIDs() (map[string]struct{}, error) {
	files, err := s.daysDescending(s.inboxDir())
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{})
	for _, f := range files {
		msgs, err := readInboxFile(f)
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			if m.EventID != "" {
				out[m.EventID] = struct{}{}
			}
		}
	}
	return out, nil
}

// SelfWrapIDs returns the set of distinct self-copy wrap event ids the
// local gate has published (Sent.SelfEventID).
//
// Used by the daemon at startup to rebuild its in-memory selfWrapIDs
// map so a relay echoing back our own self-copy wrap after daemon
// restart doesn't trip the inbound path and re-persist our outbox into
// our inbox under our own pubkey. Mirrors the runtime guard set up by
// recordSelfWrap, which records ONLY the self-copy event id.
//
// We deliberately skip Sent.EventID — the recipient-bound wrap. For a
// cross-addressed send (Bob → Alice) it never returns to Bob's
// subscription, so hydrating it is harmless but pointless. For a
// self-addressed send (Bob → Bob) it IS the legitimate delivery the
// subscription is meant to dispatch as a self-chat / self-command;
// adding it to selfWrapIDs would silently drop the operator's own
// messages after a restart.
func (s *Store) SelfWrapIDs() (map[string]struct{}, error) {
	files, err := s.daysDescending(s.outboxDir())
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{})
	for _, f := range files {
		fp, err := os.Open(f)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(fp)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			var o Sent
			if err := json.Unmarshal(sc.Bytes(), &o); err != nil {
				fp.Close()
				return nil, fmt.Errorf("decode %s: %w", f, err)
			}
			if o.SelfEventID != "" {
				out[o.SelfEventID] = struct{}{}
			}
		}
		if err := sc.Err(); err != nil {
			fp.Close()
			return nil, err
		}
		fp.Close()
	}
	return out, nil
}

func (s *Store) daysDescending(base string) ([]string, error) {
	var paths []string
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return paths, nil
	} else if err != nil {
		return nil, err
	}
	err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".jsonl" {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	return paths, err
}
