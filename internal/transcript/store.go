package transcript

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/fileops"
)

// Defaults for rotation. Overrideable via the gate's [mindform] config.
const (
	DefaultMaxCount = 50
	DefaultMaxBytes = int64(100 * 1024 * 1024) // 100 MiB
)

// IndexFile / CurrentSymlink are the well-known names inside the
// transcripts directory.
const (
	IndexFile      = "index.json"
	CurrentSymlink = "current"
)

const indexSchemaV = 1

// Index is the on-disk schema of index.json.
type Index struct {
	V     int     `json:"v"`
	Wakes []Entry `json:"wakes"`
}

// Entry is one row in Index.Wakes — a finalized wake's metadata.
type Entry struct {
	ID             string   `json:"id"`
	SessionID      string   `json:"session_id,omitempty"`
	Reason         string   `json:"reason"`
	StartedAt      int64    `json:"started_at"`
	EndedAt        int64    `json:"ended_at"`
	OK             bool     `json:"ok"`
	ExitCode       int      `json:"exit_code"`
	FailKind       string   `json:"fail_kind,omitempty"` // claudeexec.ClaudeErrorKind.String() when !OK
	CostUSD        *float64 `json:"cost_usd"`
	ToolUseCount   int      `json:"tool_use_count"`
	ThinkingBlocks int      `json:"thinking_blocks"`
	SizeBytes      int64    `json:"size_bytes"`
}

// Store manages a transcripts directory. agent-runner is the single
// writer (enforced by /eidos/run/agent.lock); this type does no internal
// locking. Readers (transcript-tail) read independently.
type Store struct {
	Dir string
}

// NewStore returns a Store for dir, creating the directory (mode 0700)
// if absent.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir transcripts: %w", err)
	}
	return &Store{Dir: dir}, nil
}

// WakePath returns the absolute ndjson path for the given wake id.
func (s *Store) WakePath(wakeID string) string {
	return filepath.Join(s.Dir, "wake-"+wakeID+".ndjson")
}

// IndexPath returns the absolute path to index.json.
func (s *Store) IndexPath() string { return filepath.Join(s.Dir, IndexFile) }

// CurrentPath returns the absolute path to the `current` symlink.
func (s *Store) CurrentPath() string { return filepath.Join(s.Dir, CurrentSymlink) }

// Open creates the per-wake ndjson file (O_CREATE|O_EXCL — collisions
// are extremely unlikely under the wake-dir flock; if they do occur,
// callers can retry with a suffix). Atomically (re)points the `current`
// symlink at the new file.
//
// Returns the open writer; caller must Close() and then call Finalize.
func (s *Store) Open(wakeID string) (*os.File, error) {
	path := s.WakePath(wakeID)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create transcript: %w", err)
	}
	if err := s.swapCurrent("wake-" + wakeID + ".ndjson"); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return f, nil
}

// swapCurrent atomically (re)points `current` at target via a tmp
// symlink + rename. Idempotent.
func (s *Store) swapCurrent(target string) error {
	tmp := s.CurrentPath() + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("symlink tmp: %w", err)
	}
	if err := os.Rename(tmp, s.CurrentPath()); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename current: %w", err)
	}
	return nil
}

// ClearCurrent removes the `current` symlink. No-op if absent.
func (s *Store) ClearCurrent() error {
	err := os.Remove(s.CurrentPath())
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// ReadIndex loads index.json. A missing file yields an empty Index
// without error; a corrupt file returns the parse error.
func (s *Store) ReadIndex() (Index, error) {
	body, err := os.ReadFile(s.IndexPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Index{V: indexSchemaV}, nil
		}
		return Index{}, fmt.Errorf("read index: %w", err)
	}
	var idx Index
	if err := json.Unmarshal(body, &idx); err != nil {
		return Index{}, fmt.Errorf("unmarshal index: %w", err)
	}
	if idx.V == 0 {
		idx.V = indexSchemaV
	}
	return idx, nil
}

// WriteIndex persists idx atomically (tmp + rename).
func (s *Store) WriteIndex(idx Index) error {
	if idx.V == 0 {
		idx.V = indexSchemaV
	}
	sort.Slice(idx.Wakes, func(i, j int) bool { return idx.Wakes[i].StartedAt > idx.Wakes[j].StartedAt })
	body, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}
	if err := fileops.AtomicWrite(s.IndexPath(), body, 0o600); err != nil {
		return fmt.Errorf("write index: %w", err)
	}
	return nil
}

// Finalize is the writer's commit step at the end of a wake: stat the
// transcript file for SizeBytes, append (or replace) the entry in
// index.json, drop the `current` symlink, and run rotation. Idempotent
// on the index entry — re-finalising the same id replaces the existing
// row.
func (s *Store) Finalize(entry Entry, maxCount int, maxBytes int64) error {
	if info, err := os.Stat(s.WakePath(entry.ID)); err == nil {
		entry.SizeBytes = info.Size()
	}
	idx, err := s.ReadIndex()
	if err != nil {
		return err
	}
	idx.Wakes = upsertEntry(idx.Wakes, entry)
	if err := s.WriteIndex(idx); err != nil {
		return err
	}
	if err := s.ClearCurrent(); err != nil {
		return err
	}
	return s.PruneToLimits(maxCount, maxBytes)
}

func upsertEntry(wakes []Entry, e Entry) []Entry {
	for i, w := range wakes {
		if w.ID == e.ID {
			wakes[i] = e
			return wakes
		}
	}
	return append(wakes, e)
}

// PruneToLimits drops oldest entries (by StartedAt) until the count and
// byte budgets are satisfied, removing both the index row and the
// underlying ndjson file. A zero/negative limit means "no cap".
func (s *Store) PruneToLimits(maxCount int, maxBytes int64) error {
	idx, err := s.ReadIndex()
	if err != nil {
		return err
	}
	// Sort newest-first; oldest at the end.
	sort.Slice(idx.Wakes, func(i, j int) bool { return idx.Wakes[i].StartedAt > idx.Wakes[j].StartedAt })

	dropped := false
	for {
		if maxCount > 0 && len(idx.Wakes) > maxCount {
			last := idx.Wakes[len(idx.Wakes)-1]
			idx.Wakes = idx.Wakes[:len(idx.Wakes)-1]
			_ = os.Remove(s.WakePath(last.ID))
			dropped = true
			continue
		}
		if maxBytes > 0 {
			var total int64
			for _, w := range idx.Wakes {
				total += w.SizeBytes
			}
			if total > maxBytes && len(idx.Wakes) > 0 {
				last := idx.Wakes[len(idx.Wakes)-1]
				idx.Wakes = idx.Wakes[:len(idx.Wakes)-1]
				_ = os.Remove(s.WakePath(last.ID))
				dropped = true
				continue
			}
		}
		break
	}
	if dropped {
		return s.WriteIndex(idx)
	}
	return nil
}

// Recover scans the transcripts directory for orphan ndjson files (files
// not represented in index.json) and synthesises index entries marking
// them as crashed. Also clears a dangling `current` symlink that points
// at a missing target.
//
// Best-effort: on any read error, the orphan is recorded with zero
// counters; rotation will still work.
//
// Idempotent.
func (s *Store) Recover() error {
	idx, err := s.ReadIndex()
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, w := range idx.Wakes {
		known[w.ID] = true
	}

	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return fmt.Errorf("read transcripts dir: %w", err)
	}
	added := false
	for _, ent := range entries {
		name := ent.Name()
		if !strings.HasPrefix(name, "wake-") || !strings.HasSuffix(name, ".ndjson") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "wake-"), ".ndjson")
		if known[id] {
			continue
		}
		path := filepath.Join(s.Dir, name)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		mtime := info.ModTime().Unix()

		// Best-effort: read the orphan to recover counters and reason.
		entry := Entry{
			ID:        id,
			StartedAt: mtime, // upper bound: we don't know the actual start
			EndedAt:   mtime,
			OK:        false,
			ExitCode:  -1,
			SizeBytes: info.Size(),
		}
		if c, reason := scanOrphan(path); c != nil {
			entry.ToolUseCount = c.ToolUseCount
			entry.ThinkingBlocks = c.ThinkingBlocks
			if c.Result != nil {
				entry.OK = c.Result.OK
				entry.CostUSD = c.Result.TotalCostUSD
			}
			if reason != "" {
				entry.Reason = reason
			}
		}
		idx.Wakes = upsertEntry(idx.Wakes, entry)
		known[id] = true // freshly recovered → treat as known so the
		// symlink-clean block below sees this wake as final.
		added = true
	}
	if added {
		if err := s.WriteIndex(idx); err != nil {
			return err
		}
	}

	// Clear dangling current symlink. Three cases lead to removal:
	//   1. Target file is missing → unambiguously stale.
	//   2. Target exists AND is in the index (whether it was already
	//      finalised or just synthesised by Recover above) → the writer
	//      is gone, so the symlink is stale.
	//   3. Otherwise leave it alone (a live wake's writer may legitimately
	//      hold the symlink open).
	if target, err := os.Readlink(s.CurrentPath()); err == nil {
		if _, err := os.Stat(filepath.Join(s.Dir, target)); errors.Is(err, fs.ErrNotExist) {
			_ = os.Remove(s.CurrentPath())
		} else if err == nil {
			id := strings.TrimSuffix(strings.TrimPrefix(target, "wake-"), ".ndjson")
			if known[id] {
				_ = os.Remove(s.CurrentPath())
			}
		}
	}
	return nil
}

// scanOrphan re-parses an ndjson file to recover counters and (best-
// effort) the wake reason. Reason is not in the stream-json output —
// it's set by the caller at Open time. We leave it empty for orphans
// (the wake id encodes it as `<unix>-<reason>`, so the host renderer
// can extract it from the id when the index has reason="").
func scanOrphan(path string) (*Counter, string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, ""
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20) // 1 MiB lines tolerated
	c := &Counter{}
	for {
		line, err := readLine(r)
		if len(line) > 0 {
			if ev, perr := ParseEvent(line); perr == nil {
				c.Observe(ev)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
	}
	return c, ""
}

// readLine reads one '\n'-terminated line from r without a hard upper
// bound. A trailing partial line at EOF is returned with (line, io.EOF).
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return buf, err
	}
}
