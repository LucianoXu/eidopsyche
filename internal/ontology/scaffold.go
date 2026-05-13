package ontology

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

// templateFuncs are exposed inside every .tpl rendered by scaffold/tar.
// `tomlstr` quotes an arbitrary string as a TOML basic string so
// identity.toml.tpl can safely interpolate labels / npubs / etc. that
// would otherwise break the file when they contain `"`, `\`, or
// control bytes. json.Marshal produces a Unicode-escaped, double-
// quoted string whose syntax overlaps TOML's basic-string syntax for
// all UTF-8 inputs.
var templateFuncs = template.FuncMap{
	"tomlstr": func(s string) (string, error) {
		b, err := json.Marshal(s)
		if err != nil {
			return "", err
		}
		return string(b), nil
	},
}

// Params are the substitutions Scaffold and TarStream make into .tpl
// files. The full set is rendered into every identity.toml.tpl and is
// also available to other prefab-authored .tpl files (calling-words,
// CLAUDE.md, soul). Unused fields render as the empty string unless
// the renderer is configured with missingkey=error (which the prefab
// tar stream does, see prefab.go) — so prefab .tpl files referencing
// an undeclared key fail loud rather than ship blank substitutions.
type Params struct {
	Label        string
	OwnerNpub    string
	OwnerLabel   string
	MindFormNpub string
	HomeRelay    string
	CreatedDate  string

	// SummoningBook, if non-empty, is appended to the tar stream produced
	// by TarStream and TarStreamPrefab as a literal file at
	// chest/summoning-book.md. It is NOT run through text/template —
	// the wizard's pre-rendered markdown can contain `{{` literals that
	// would otherwise break the template engine. chest/ is .gitignored
	// inside the ontology so the seal lives near the mind-form but does
	// not enter their persistent life-log.
	SummoningBook string

	// RoleResearch, if non-empty, is appended to the tar stream produced
	// by TarStream as a literal file at self/role-research.md. The
	// wizard's scratch path renders this with claude (player description
	// + web research); the prefab path ships a pre-authored copy as a
	// regular file inside prefab/<id>/self/ instead.
	RoleResearch string
}

// Scaffold writes the v0 ontology template into dir, rendering any .tpl
// files with params. dir must be empty or the function refuses.
func Scaffold(dir string, params Params) error {
	if err := assertEmpty(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir target: %w", err)
	}
	tfs := templateFS()
	return fs.WalkDir(tfs, "template", func(srcPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("template", srcPath)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dst := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		body, err := fs.ReadFile(tfs, srcPath)
		if err != nil {
			return fmt.Errorf("read embed %s: %w", srcPath, err)
		}
		// Render .tpl files; trim suffix from destination.
		if strings.HasSuffix(dst, ".tpl") {
			dst = strings.TrimSuffix(dst, ".tpl")
			rendered, err := renderTemplate(string(body), params)
			if err != nil {
				return fmt.Errorf("render %s: %w", srcPath, err)
			}
			body = []byte(rendered)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return fmt.Errorf("mkdir parent of %s: %w", dst, err)
		}
		if err := os.WriteFile(dst, body, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
		return nil
	})
}

// TarStream writes a tar of the rendered template tree to w. Used by
// `eidos forge create` to pipe the template into a one-shot init container
// over stdin.
func TarStream(w io.Writer, params Params) error {
	tw := tar.NewWriter(w)
	now := time.Now()
	tfs := templateFS()
	walkErr := fs.WalkDir(tfs, "template", func(srcPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// embed.FS paths always use forward slashes (io/fs contract), so
		// TrimPrefix is correct on every platform, unlike filepath.Rel.
		name := strings.TrimPrefix(srcPath, "template/")
		if name == "template" || name == "" {
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
		body, err := fs.ReadFile(tfs, srcPath)
		if err != nil {
			return err
		}
		if strings.HasSuffix(name, ".tpl") {
			name = strings.TrimSuffix(name, ".tpl")
			rendered, err := renderTemplate(string(body), params)
			if err != nil {
				return err
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
		if _, err := tw.Write(body); err != nil {
			return err
		}
		return nil
	})
	if walkErr != nil {
		tw.Close() //nolint:errcheck // best-effort; return the walk error
		return walkErr
	}
	if params.SummoningBook != "" {
		body := []byte(params.SummoningBook)
		if err := tw.WriteHeader(&tar.Header{
			Name:     "chest/summoning-book.md",
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
	if params.RoleResearch != "" {
		body := []byte(params.RoleResearch)
		if err := tw.WriteHeader(&tar.Header{
			Name:     "self/role-research.md",
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

func assertEmpty(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat target: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("target %s is not empty (%d entries)", dir, len(entries))
	}
	return nil
}

func renderTemplate(body string, params Params) (string, error) {
	t, err := template.New("ontology").Funcs(templateFuncs).Parse(body)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := t.Execute(&sb, params); err != nil {
		return "", err
	}
	return sb.String(), nil
}
