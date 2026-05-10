package ontology

import (
	"archive/tar"
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

// Params are the substitutions Scaffold and TarStream make into .tpl
// files. Existing template/ .tpl files only reference Label / OwnerNpub
// / CreatedDate; the additional fields are populated for the prefab
// path so prefab .tpl files can address master / mind-form by label
// and pubkey. Unused fields render as zero (an empty string), unless
// the renderer is configured with missingkey=error (which the prefab
// tar stream does, see prefab.go).
type Params struct {
	// Required by both paths.
	Label       string
	OwnerNpub   string
	CreatedDate string

	// Used only by prefab .tpl files; ignored by template/.
	OwnerLabel   string
	MindFormNpub string
	HomeRelay    string

	// JournalEntry, if non-empty, is appended to the tar stream produced
	// by TarStream as a literal file at journal/0000-summoning.md. It is
	// NOT run through text/template — the wizard's pre-rendered markdown
	// can contain `{{` literals that would otherwise break the template
	// engine. Used by the First Contact wizard's scratch path; prefab
	// path leaves this empty.
	JournalEntry string
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
	t, err := template.New("ontology").Parse(body)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := t.Execute(&sb, params); err != nil {
		return "", err
	}
	return sb.String(), nil
}
