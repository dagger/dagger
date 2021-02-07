package dagger

import (
	"context"
	"path"
	"path/filepath"

	cueerrors "cuelang.org/go/cue/errors"
	cueload "cuelang.org/go/cue/load"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"dagger.cloud/go/dagger/cc"
)

// Build a cue configuration tree from the files in fs.
func CueBuild(ctx context.Context, fs FS, args ...string) (*cc.Value, error) {
	lg := log.Ctx(ctx)

	// The CUE overlay needs to be prefixed by a non-conflicting path with the
	// local filesystem, otherwise Cue will merge the Overlay with whatever Cue
	// files it finds locally.
	const overlayPrefix = "/config"

	buildConfig := &cueload.Config{
		Dir:     overlayPrefix,
		Overlay: map[string]cueload.Source{},
	}
	buildArgs := args

	err := fs.Walk(ctx, func(p string, f Stat) error {
		lg.Debug().Str("path", p).Msg("Compiler.Build: processing")
		if f.IsDir() {
			return nil
		}
		if filepath.Ext(p) != ".cue" {
			return nil
		}
		contents, err := fs.ReadFile(ctx, p)
		if err != nil {
			return errors.Wrap(err, p)
		}
		overlayPath := path.Join(overlayPrefix, p)
		buildConfig.Overlay[overlayPath] = cueload.FromBytes(contents)
		return nil
	})
	if err != nil {
		return nil, err
	}
	instances := cueload.Instances(buildArgs, buildConfig)
	if len(instances) != 1 {
		return nil, errors.New("only one package is supported at a time")
	}
	inst, err := cc.Cue().Build(instances[0])
	if err != nil {
		return nil, errors.New(cueerrors.Details(err, &cueerrors.Config{}))
	}
	return cc.Wrap(inst.Value(), inst), nil
}
