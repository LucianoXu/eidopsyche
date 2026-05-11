// Package state defines the state authority abstractions used by the
// daemon: a Tree of named contributors that `state.get` snapshots
// against, and an apply hook registry that the Mutate helper dispatches
// against after each successful write.
//
// internal/state is intentionally dependency-free of cron / lifecycle /
// daemon. Apply hooks receive `deps any`; the package that registers a
// hook defines its own concrete deps shape and casts on entry. This
// keeps a single-direction import arrow: lifecycle → cron, daemon → state
// → config; nothing in state imports lifecycle, cron, or daemon.
package state

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
)

// ErrPathNotFound is returned by Tree.Snapshot when the requested
// dotted path doesn't match any contributor or any field within a
// contributor's snapshot.
var ErrPathNotFound = errors.New("state path not found")

// StateContributor exposes a named subtree of the daemon's state. Each
// contributor owns one mount path (which may itself be dotted, e.g.
// "lifecycle.wakes"); the Tree mounts contributors at their declared
// paths so the root-snapshot view nests cleanly.
type StateContributor interface {
	Path() string
	Snapshot(ctx context.Context) (any, error)
}

// Tree resolves dotted-path queries against registered contributors.
// Concurrent-safe; Register and Snapshot may be called from any goroutine.
type Tree struct {
	mu     sync.RWMutex
	contrs []StateContributor // longest-path first for deterministic prefix matching
}

// NewTree returns an empty Tree.
func NewTree() *Tree { return &Tree{} }

// Register adds a contributor at its declared Path(). Duplicate paths
// panic — registration is a startup-time correctness concern.
func (t *Tree) Register(c StateContributor) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, existing := range t.contrs {
		if existing.Path() == c.Path() {
			panic("state: duplicate contributor at path " + c.Path())
		}
	}
	t.contrs = append(t.contrs, c)
	sort.Slice(t.contrs, func(i, j int) bool {
		return len(t.contrs[i].Path()) > len(t.contrs[j].Path())
	})
}

// Snapshot resolves a dotted path. Empty path returns the full root tree.
// path == contributor.Path() returns that contributor's snapshot verbatim.
// path under a contributor walks the snapshot via map indexing or
// struct field reflection.
//
// Returns ErrPathNotFound when nothing matches.
func (t *Tree) Snapshot(ctx context.Context, path string) (any, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if path == "" {
		return t.fullRoot(ctx)
	}
	for _, c := range t.contrs {
		cp := c.Path()
		if path == cp {
			return c.Snapshot(ctx)
		}
		if strings.HasPrefix(path, cp+".") {
			snap, err := c.Snapshot(ctx)
			if err != nil {
				return nil, fmt.Errorf("%s contributor: %w", cp, err)
			}
			remainder := strings.TrimPrefix(path, cp+".")
			v, ok := walk(snap, remainder)
			if !ok {
				return nil, ErrPathNotFound
			}
			return v, nil
		}
	}
	return nil, ErrPathNotFound
}

// fullRoot assembles every contributor into a single nested map.
// Fail-fast: if any contributor errors, the whole snapshot fails
// (avoids misleading partial views). Per the spec §7.5.
//
// Mount order matters when contributors register at nested paths
// (e.g. both "lifecycle" and "lifecycle.wakes"). If we mounted the
// shorter path last, its scalar/map value would overwrite the
// intermediate map mount() created for the longer path. We iterate
// shortest-path-first (reverse of t.contrs's longest-first order)
// so shallow contributors land first and deep contributors compose
// onto the intermediate maps. Snapshot's prefix matching still uses
// the longest-first order so the most specific contributor wins.
func (t *Tree) fullRoot(ctx context.Context) (any, error) {
	out := map[string]any{}
	for i := len(t.contrs) - 1; i >= 0; i-- {
		c := t.contrs[i]
		snap, err := c.Snapshot(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s contributor: %w", c.Path(), err)
		}
		mount(out, c.Path(), snap)
	}
	return out, nil
}

// mount inserts v at the dotted path within root, creating
// intermediate maps as needed.
func mount(root map[string]any, dotted string, v any) {
	parts := strings.Split(dotted, ".")
	m := root
	for i := 0; i < len(parts)-1; i++ {
		next, ok := m[parts[i]].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[parts[i]] = next
		}
		m = next
	}
	m[parts[len(parts)-1]] = v
}

// walk descends v along the dotted remainder. Supports map[string]any
// indexing and struct field reflection (case-insensitive on field name
// or toml/json tags). Returns (value, true) on success, (nil, false) on
// miss.
func walk(v any, remainder string) (any, bool) {
	cur := v
	for _, segment := range strings.Split(remainder, ".") {
		next, ok := step(cur, segment)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func step(v any, segment string) (any, bool) {
	if m, ok := v.(map[string]any); ok {
		x, hit := m[segment]
		return x, hit
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, false
		}
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Struct {
		for i := 0; i < rv.NumField(); i++ {
			f := rv.Type().Field(i)
			if !f.IsExported() {
				continue
			}
			if strings.EqualFold(f.Name, segment) ||
				strings.EqualFold(firstTag(f.Tag.Get("toml")), segment) ||
				strings.EqualFold(firstTag(f.Tag.Get("json")), segment) {
				return rv.Field(i).Interface(), true
			}
		}
	}
	return nil, false
}

func firstTag(t string) string {
	if i := strings.IndexByte(t, ','); i >= 0 {
		return t[:i]
	}
	return t
}
