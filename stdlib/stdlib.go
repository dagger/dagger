package stdlib

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var (
	// FS contains the filesystem of the stdlib.
	//go:embed *
	FS embed.FS

	PackageName = "alpha.dagger.io"
	Path        = path.Join("cue.mod", "pkg", PackageName)
)

func Vendor(ctx context.Context, mod string) error {
	// Remove any existing copy of the universe
	if err := os.RemoveAll(path.Join(mod, Path)); err != nil {
		return err
	}

	// Write the current version
	return fs.WalkDir(FS, ".", func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !entry.Type().IsRegular() {
			return nil
		}

		if strings.HasPrefix(p, ".dagger") || strings.HasPrefix(p, "node_modules") || strings.HasPrefix(p, "cue.mod") {
			return nil
		}

		contents, err := fs.ReadFile(FS, p)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}

		overlayPath := path.Join(mod, Path, p)

		if err := os.MkdirAll(filepath.Dir(overlayPath), 0755); err != nil {
			return err
		}

		// FIXME: giant hack. permissions don't seem to be preserved by
		// go:embed so we set 0700 to make sure we don't lose bits.
		// #nosec
		return os.WriteFile(overlayPath, contents, 0700)
	})
}
