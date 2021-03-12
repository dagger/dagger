package compiler

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"

	cueerrors "cuelang.org/go/cue/errors"
	cueload "cuelang.org/go/cue/load"
)

// Build a cue configuration tree from the files in fs.
func Build(sources map[string]fs.FS, args ...string) (*Value, error) {
	buildConfig := &cueload.Config{
		// The CUE overlay needs to be prefixed by a non-conflicting path with the
		// local filesystem, otherwise Cue will merge the Overlay with whatever Cue
		// files it finds locally.
		Dir:     "/config",
		Overlay: map[string]cueload.Source{},
	}

	// Map the source files into the overlay
	for mnt, f := range sources {
		f := f
		mnt := mnt
		err := fs.WalkDir(f, ".", func(p string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if !entry.Type().IsRegular() {
				return nil
			}

			if filepath.Ext(entry.Name()) != ".cue" {
				return nil
			}

			contents, err := fs.ReadFile(f, p)
			if err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}

			overlayPath := path.Join(buildConfig.Dir, mnt, p)
			buildConfig.Overlay[overlayPath] = cueload.FromBytes(contents)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	instances := cueload.Instances(args, buildConfig)
	if len(instances) != 1 {
		return nil, errors.New("only one package is supported at a time")
	}
	inst, err := Cue().Build(instances[0])
	if err != nil {
		return nil, errors.New(cueerrors.Details(err, &cueerrors.Config{}))
	}
	return Wrap(inst.Value(), inst), nil
}
