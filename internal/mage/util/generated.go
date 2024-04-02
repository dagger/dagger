package util

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
)

// LintGeneratedCode ensures the generated code is up to date.
//
// 1) Read currently generated code
// 2) Generate again
// 3) Compare
// 4) Restore original generated code.
func LintGeneratedCode(target string, fn func() error, files ...string) error {
	newFiles := make([]string, 0, len(files))
	for _, file := range files {
		err := filepath.WalkDir(file, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			newFiles = append(newFiles, path)
			return nil
		})
		if err != nil {
			return err
		}
	}
	files = newFiles

	originals := map[string][]byte{}
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("could not read original file: %w", err)
		}
		originals[f] = content
	}

	defer func() {
		for _, f := range files {
			defer os.WriteFile(f, originals[f], 0600)
		}
	}()

	if err := fn(); err != nil {
		return err
	}

	for _, f := range files {
		original := string(originals[f])
		updated, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("could not read updated file: %w", err)
		}

		if original != string(updated) {
			edits := myers.ComputeEdits(span.URIFromPath(f), original, string(updated))
			diff := fmt.Sprint(gotextdiff.ToUnified(f, f, original, edits))
			return fmt.Errorf("Generated code mismatch. Please run `%s`:\n%s", target, diff)
		}
	}

	return nil
}
