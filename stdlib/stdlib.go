package stdlib

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"

	cueload "cuelang.org/go/cue/load"
)

// FS contains the filesystem of the stdlib.
//go:embed **/*.cue **/*/*.cue
var FS embed.FS

const (
	stdlibPackageName = "dagger.io"
)

func Overlay(prefixPath string) (map[string]cueload.Source, error) {
	overlay := map[string]cueload.Source{}

	err := fs.WalkDir(FS, ".", func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !entry.Type().IsRegular() {
			return nil
		}

		if filepath.Ext(entry.Name()) != ".cue" {
			return nil
		}

		contents, err := FS.ReadFile(p)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}

		overlayPath := path.Join(prefixPath, "cue.mod", "pkg", stdlibPackageName, p)

		overlay[overlayPath] = cueload.FromBytes(contents)
		return nil
	})

	return overlay, err
}
