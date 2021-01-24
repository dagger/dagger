//go:generate sh gen.sh
package dagger

import (
	"context"
	"path"
	"path/filepath"
	"sync"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
	cueload "cuelang.org/go/cue/load"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// Polyfill for a cue runtime
// (we call it compiler to avoid confusion with dagger runtime)
// Use this instead of cue.Runtime
type Compiler struct {
	sync.RWMutex
	cue.Runtime
	spec *Spec
}

func (cc *Compiler) Cue() *cue.Runtime {
	return &(cc.Runtime)
}

func (cc *Compiler) Spec() *Spec {
	if cc.spec != nil {
		return cc.spec
	}
	v, err := cc.Compile("spec.cue", DaggerSpec)
	if err != nil {
		panic(err)
	}
	cc.spec, err = newSpec(v)
	if err != nil {
		panic(err)
	}
	return cc.spec
}

// Compile an empty struct
func (cc *Compiler) EmptyStruct() (*Value, error) {
	return cc.Compile("", "")
}

func (cc *Compiler) Compile(name string, src interface{}) (*Value, error) {
	inst, err := cc.Cue().Compile(name, src)
	if err != nil {
		// FIXME: cleaner way to unwrap cue error details?
		return nil, cueErr(err)
	}
	return cc.Wrap(inst.Value(), inst), nil
}

// Compile a cue configuration, and load it as a script.
// If the cue configuration is invalid, or does not match the script spec,
//  return an error.
func (cc *Compiler) CompileScript(name string, src interface{}) (*Script, error) {
	v, err := cc.Compile(name, src)
	if err != nil {
		return nil, err
	}
	return NewScript(v)
}

// Build a cue configuration tree from the files in fs.
func (cc *Compiler) Build(ctx context.Context, fs FS, args ...string) (*Value, error) {
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

func (cc *Compiler) Wrap(v cue.Value, inst *cue.Instance) *Value {
	return wrapValue(v, inst, cc)
}
