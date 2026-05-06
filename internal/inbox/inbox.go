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
func (s *Store) ListInbox(since *time.Time, from string, limit int) ([]Message, error) {
	files, err := s.daysDescending(s.inboxDir())
	if err != nil {
		return nil, err
	}
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
