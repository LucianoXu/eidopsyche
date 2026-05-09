package ontology

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// Params are the substitutions Scaffold makes into .tpl files.
type Params struct {
	Label       string
	OwnerNpub   string
	CreatedDate string
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
	return fs.WalkDir(templateFS, "template", func(srcPath string, d fs.DirEntry, err error) error {
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
		body, err := fs.ReadFile(templateFS, srcPath)
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
