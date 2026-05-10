package inbox

import (
	"bufio"
	"encoding/json"
	"os"
	"time"
)

func (s *Store) AppendOutbox(o Sent) error {
	if o.V == 0 {
		o.V = 1
	}
	if o.SentAt == 0 {
		o.SentAt = time.Now().Unix()
	}
	o.Label = "" // transit-only; defense against accidental persistence
	return s.appendJSONL(dailyPath(s.outboxDir(), time.Unix(o.SentAt, 0)), o)
}

// ListOutbox collapses rows by EventID; later rows override earlier ones; rows
// with Final=true take precedence. Ack fields (AckedAt / AckEventID) are
// merged independently with first-ack-wins semantics so they survive
// regardless of row ordering relative to the Final-true publish-finalization
// row. Returns newest-first.
func (s *Store) ListOutbox(since *time.Time, to string, limit int) ([]Sent, error) {
	files, err := s.daysDescending(s.outboxDir())
	if err != nil {
		return nil, err
	}
	collapsed := make(map[string]Sent)
	order := make([]string, 0)
	acks := make(map[string]Sent) // EventID → row carrying ack fields; first-ack-wins
	for i := len(files) - 1; i >= 0; i-- {
		f := files[i]
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
				return nil, err
			}
			prev, seen := collapsed[o.EventID]
			if !seen {
				order = append(order, o.EventID)
				collapsed[o.EventID] = o
			} else if o.Final || !prev.Final {
				collapsed[o.EventID] = o
			}
			// Ack overlay: first-ack-wins, independent of Final stickiness.
			if o.AckedAt != 0 {
				if _, taken := acks[o.EventID]; !taken {
					acks[o.EventID] = o
				}
			}
		}
		fp.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	for eid, a := range acks {
		row := collapsed[eid]
		row.AckedAt = a.AckedAt
		row.AckEventID = a.AckEventID
		collapsed[eid] = row
	}
	out := make([]Sent, 0, len(order))
	for _, id := range order {
		out = append(out, collapsed[id])
	}
	sortStable(out, func(i, j int) bool { return out[i].SentAt > out[j].SentAt })
	filtered := out[:0]
	for _, o := range out {
		if since != nil && time.Unix(o.SentAt, 0).Before(*since) {
			continue
		}
		if to != "" && o.To != to {
			continue
		}
		filtered = append(filtered, o)
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}
	return filtered, nil
}

func sortStable[T any](s []T, less func(i, j int) bool) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(j, j-1); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
