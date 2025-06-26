package generator

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func Overlay(ctx context.Context, logsW io.Writer, overlay fs.FS, outputDir string) (rerr error) {
	return fs.WalkDir(overlay, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if _, err := os.Stat(filepath.Join(outputDir, path)); err == nil {
				fmt.Fprintln(logsW, "creating directory", path, "[skipped]")
				return nil
			}
			fmt.Fprintln(logsW, "creating directory", path)
			return os.MkdirAll(filepath.Join(outputDir, path), 0o755)
		}

		var needsWrite bool

		newContent, err := fs.ReadFile(overlay, path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		outPath := filepath.Join(outputDir, path)
		oldContent, err := os.ReadFile(outPath)
		if err != nil {
			needsWrite = true
		} else {
			needsWrite = string(oldContent) != string(newContent)
		}

		if !needsWrite {
			fmt.Fprintln(logsW, "writing", path, "[skipped]")
			return nil
		}

		fmt.Fprintln(logsW, "writing", path)
		return os.WriteFile(outPath, newContent, 0o600)
	})
}