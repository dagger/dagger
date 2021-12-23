package stdlib

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

var (
	// FS contains the filesystem of the stdlib.
	//go:embed **/*.cue **/*/*.cue europa/dagger/*.cue europa/dagger/engine/*.cue
	FS embed.FS

	ModuleName    = "alpha.dagger.io"
	EnginePackage = fmt.Sprintf("%s/europa/dagger/engine", ModuleName)
	Path          = path.Join("cue.mod", "pkg", ModuleName)
)

func Vendor(ctx context.Context, dest string) error {
	// Write the current version
	return fs.WalkDir(FS, ".", func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !entry.Type().IsRegular() {
			return nil
		}

		if filepath.Ext(entry.Name()) != ".cue" {
			return nil
		}

		contents, err := fs.ReadFile(FS, p)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}

		overlayPath := path.Join(dest, p)

		if err := os.MkdirAll(filepath.Dir(overlayPath), 0755); err != nil {
			return err
		}

		return os.WriteFile(overlayPath, contents, 0600)
	})
}
