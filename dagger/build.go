package dagger

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	cueerrors "cuelang.org/go/cue/errors"
	cueload "cuelang.org/go/cue/load"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"dagger.cloud/go/dagger/cc"
	"dagger.cloud/go/stdlib"
)

// Build a cue configuration tree from the files in fs.
func CueBuild(ctx context.Context, fs FS, args ...string) (*cc.Value, error) {
	var (
		err error
		lg  = log.Ctx(ctx)
	)

	buildConfig := &cueload.Config{
		// The CUE overlay needs to be prefixed by a non-conflicting path with the
		// local filesystem, otherwise Cue will merge the Overlay with whatever Cue
		// files it finds locally.
		Dir: "/config",
	}

	// Start by creating an overlay with the stdlib
	buildConfig.Overlay, err = stdlib.Overlay(buildConfig.Dir)
	if err != nil {
		return nil, err
	}

	// Add the config files on top of the overlay
	err = fs.Walk(ctx, func(p string, f Stat) error {
		lg.Debug().Str("path", p).Msg("Compiler.Build: processing")
		if f.IsDir() {
			return nil
		}
		if filepath.Ext(p) != ".cue" {
			return nil
		}
		contents, err := fs.ReadFile(ctx, p)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		overlayPath := path.Join(buildConfig.Dir, p)
		buildConfig.Overlay[overlayPath] = cueload.FromBytes(contents)
		return nil
	})
	if err != nil {
		return nil, err
	}
	instances := cueload.Instances(args, buildConfig)
	if len(instances) != 1 {
		return nil, errors.New("only one package is supported at a time")
	}
	inst, err := cc.Cue().Build(instances[0])
	if err != nil {
		return nil, errors.New(cueerrors.Details(err, &cueerrors.Config{}))
	}
	return cc.Wrap(inst.Value(), inst), nil
}
