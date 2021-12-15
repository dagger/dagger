package stdlib

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/gofrs/flock"
)

var (
	// FS contains the filesystem of the stdlib.
	//go:embed **/*.cue **/*/*.cue europa/dagger/*.cue europa/dagger/engine/*.cue europa/dagger/engine/spec/*.cue europa/dagger/engine/spec/engine/*.cue
	FS embed.FS

	ModuleName    = "alpha.dagger.io"
	EnginePackage = fmt.Sprintf("%s/europa/dagger/engine", ModuleName)
	Path          = path.Join("cue.mod", "pkg", ModuleName)
	lockFilePath  = path.Join("cue.mod", "dagger.lock")
)

func Vendor(ctx context.Context, mod string) error {
	lockFilePath := path.Join(mod, lockFilePath)
	fileLock := flock.New(lockFilePath)
	if err := fileLock.Lock(); err != nil {
		return err
	}

	defer func() {
		fileLock.Unlock()
		os.Remove(lockFilePath)
	}()

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

		if filepath.Ext(entry.Name()) != ".cue" {
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

		return os.WriteFile(overlayPath, contents, 0600)
	})
}
