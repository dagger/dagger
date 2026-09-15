package generator

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Apply removes obsolete generated paths and then writes the current overlay.
func Apply(ctx context.Context, generated *GeneratedState, outputDir string) error {
	for _, path := range generated.RemovePaths {
		path = filepath.Clean(path)
		if path == "." || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
			return fmt.Errorf("refusing to remove path outside output directory: %q", path)
		}

		outPath := filepath.Join(outputDir, path)
		if err := os.Remove(outPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		slog.Info("removing", "path", path)
	}

	return Overlay(ctx, generated.Overlay, outputDir)
}

func Overlay(ctx context.Context, overlay fs.FS, outputDir string) (rerr error) {
	return fs.WalkDir(overlay, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if _, err := os.Stat(filepath.Join(outputDir, path)); err == nil {
				slog.Info("creating directory [skipped]", "path", path)
				return nil
			}
			slog.Info("creating directory", "path", path)
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
			slog.Info("writing [skipped]", "path", path)
			return nil
		}

		slog.Info("writing", "path", path)
		return os.WriteFile(outPath, newContent, 0o600)
	})
}
