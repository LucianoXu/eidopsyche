package ontology

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/BurntSushi/toml"
)

// Meta is a parsed prefab.toml plus the resolved id (which equals the
// prefab's directory name under prefab/).
type Meta struct {
	ID      string
	Kind    string            // "m" | "f" | "spirit"
	Display map[string]string // lang → display_name
	Tagline map[string]string
	Preview map[string]string
}

// kindOrder controls List() ordering: men → women → spirits, mirroring
// NOTEBOOK.md's table layout. Unknown kinds sort last.
var kindOrder = map[string]int{
	"m":      0,
	"f":      1,
	"spirit": 2,
}

// List returns every embedded prefab whose id does not start with "_".
// Underscore-prefixed prefabs are reserved for test fixtures; they are
// embedded in the binary (so tests can access them via MetaFor /
// TarStreamPrefab) but hidden from the menu.
//
// Sort order: kindOrder (m, f, spirit, unknown), then id ascending.
// Malformed prefab.toml files are skipped — a single bad prefab does
// not take down the menu.
func List() ([]Meta, error) {
	root, err := fs.Sub(prefabFS(), "prefab")
	if err != nil {
		return nil, fmt.Errorf("prefab root: %w", err)
	}
	entries, err := fs.ReadDir(root, ".")
	if err != nil {
		return nil, fmt.Errorf("read prefab root: %w", err)
	}
	out := make([]Meta, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if strings.HasPrefix(id, "_") {
			continue
		}
		m, err := MetaFor(id)
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		ki := kindIndex(out[i].Kind)
		kj := kindIndex(out[j].Kind)
		if ki != kj {
			return ki < kj
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func kindIndex(k string) int {
	if v, ok := kindOrder[k]; ok {
		return v
	}
	return len(kindOrder)
}

// MetaFor reads prefab/<id>/prefab.toml and returns the parsed Meta.
// Returns an error if the directory or the toml file is missing or
// malformed.
func MetaFor(id string) (Meta, error) {
	body, err := fs.ReadFile(prefabFS(), "prefab/"+id+"/prefab.toml")
	if err != nil {
		return Meta{}, fmt.Errorf("read prefab.toml for %q: %w", id, err)
	}
	var raw struct {
		ID      string            `toml:"id"`
		Kind    string            `toml:"kind"`
		Display map[string]string `toml:"display"`
		Tagline map[string]string `toml:"tagline"`
		Preview map[string]string `toml:"preview"`
	}
	if err := toml.Unmarshal(body, &raw); err != nil {
		return Meta{}, fmt.Errorf("parse prefab.toml for %q: %w", id, err)
	}
	if raw.ID != "" && raw.ID != id {
		return Meta{}, fmt.Errorf("prefab %q: prefab.toml id = %q (mismatch)", id, raw.ID)
	}
	return Meta{
		ID:      id,
		Kind:    raw.Kind,
		Display: raw.Display,
		Tagline: raw.Tagline,
		Preview: raw.Preview,
	}, nil
}

// TarStreamPrefab writes a tar of the prefab/<id>/ tree to w, applying
// .tpl rendering with params. The prefab.toml sidecar is excluded from
// the archive. Templates use Option("missingkey=error") so a typo in a
// .tpl reference fails loud instead of writing a blank substitution
// into the new mind-form's volume.
func TarStreamPrefab(w io.Writer, id string, params Params) error {
	pfs := prefabFS()
	root := "prefab/" + id
	if _, err := fs.Stat(pfs, root); err != nil {
		return fmt.Errorf("prefab %q: %w", id, err)
	}
	tw := tar.NewWriter(w)
	now := time.Now()
	walkErr := fs.WalkDir(pfs, root, func(srcPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// embed.FS uses forward slashes (io/fs contract), so TrimPrefix
		// is correct cross-platform.
		name := strings.TrimPrefix(srcPath, root+"/")
		if name == root || name == "" || srcPath == root {
			return nil
		}
		if name == "prefab.toml" {
			return nil
		}
		if d.IsDir() {
			return tw.WriteHeader(&tar.Header{
				Name:     name + "/",
				Mode:     0o700,
				Typeflag: tar.TypeDir,
				ModTime:  now,
			})
		}
		body, err := fs.ReadFile(pfs, srcPath)
		if err != nil {
			return err
		}
		if strings.HasSuffix(name, ".tpl") {
			name = strings.TrimSuffix(name, ".tpl")
			rendered, err := renderTemplateStrict(string(body), params)
			if err != nil {
				return fmt.Errorf("render %s: %w", srcPath, err)
			}
			body = []byte(rendered)
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o600,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
			ModTime:  now,
		}); err != nil {
			return err
		}
		_, err = tw.Write(body)
		return err
	})
	if walkErr != nil {
		tw.Close() //nolint:errcheck // best-effort; return the walk error
		return walkErr
	}
	// Append the wizard's pre-rendered summoning book as a literal file
	// at journal/0000-summoning.md. Mirror TarStream's behaviour so the
	// supervisor's birth handler can find the file regardless of which
	// scaffold path produced the volume. Without this the prefab path
	// leaves journal/ empty and the birth handler retries forever.
	if params.JournalEntry != "" {
		body := []byte(params.JournalEntry)
		if err := tw.WriteHeader(&tar.Header{
			Name:     "journal/0000-summoning.md",
			Mode:     0o600,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
			ModTime:  now,
		}); err != nil {
			tw.Close() //nolint:errcheck
			return err
		}
		if _, err := tw.Write(body); err != nil {
			tw.Close() //nolint:errcheck
			return err
		}
	}
	return tw.Close()
}

// renderTemplateStrict is renderTemplate with missingkey=error. The
// scratch path tolerates missing keys (Params struct evolves freely);
// prefab .tpl files reference the full Params surface, so a typo in
// a prefab .tpl should fail loud rather than ship blank substitutions
// into the new mind-form's volume.
func renderTemplateStrict(body string, params Params) (string, error) {
	t, err := template.New("ontology").Option("missingkey=error").Parse(body)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := t.Execute(&sb, params); err != nil {
		return "", err
	}
	return sb.String(), nil
}
