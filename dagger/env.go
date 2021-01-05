package dagger

import (
	"context"
	"os"

	"cuelang.org/go/cue"
	cueflow "cuelang.org/go/tools/flow"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"
)

type Env struct {
	// Base config
	base *Value
	// Input overlay: user settings, external directories, secrets...
	input *Value

	// Output overlay: computed values, generated directories
	output *Value

	// Buildkit solver
	s Solver

	// shared cue compiler
	// (because cue API requires shared runtime for everything)
	cc *Compiler
}

// Initialize a new environment
func NewEnv(ctx context.Context, c bkgw.Client) (*Env, error) {
	cc := &Compiler{}
	// 1. Load base config (specified by client)
	debugf("Loading base configuration")
	base, err := envBase(ctx, c, cc)
	if err != nil {
		return nil, err
	}
	// 2. Load input overlay (specified by client)
	debugf("Loading input overlay")
	input, err := envInput(ctx, c, cc)
	if err != nil {
		return nil, err
	}
	if _, err := base.Merge(input); err != nil {
		return nil, errors.Wrap(err, "merge base & input")
	}
	return &Env{
		base:  base,
		input: input,
		s:     NewSolver(c),
		cc:    cc,
	}, nil
}

// Compute missing values in env configuration, and write them to state.
func (env *Env) Compute(ctx context.Context) error {
	debugf("Computing environment")
	output, err := env.Walk(ctx, func(c *Component, out Fillable) error {
		_, err := c.Compute(ctx, env.s, out)
		return err
	})
	if err != nil {
		return err
	}
	env.output = output
	return nil
}

// Export env to a directory of cue files
// (Use with FS.Change)
func (env *Env) Export(fs FS) (FS, error) {
	// FIXME: we serialize as JSON to guarantee a self-contained file.
	//   Value.Save() leaks imports, so requires a shared cue.mod with
	//   client which is undesirable.
	//   Once Value.Save() resolves non-builtin imports with a tree shake,
	//   we can use it here.
	fs = env.base.SaveJSON(fs, "base.cue")
	fs = env.input.SaveJSON(fs, "input.cue")
	if env.output != nil {
		fs = env.output.SaveJSON(fs, "output.cue")
	}
	return fs, nil
}

type EnvWalkFunc func(*Component, Fillable) error

// Walk components and return any computed values
func (env *Env) Walk(ctx context.Context, fn EnvWalkFunc) (*Value, error) {
	debugf("Env.Walk")
	defer debugf("COMPLETE: Env.Walk")
	// Cueflow cue instance
	// FIXME: make this cleaner in *Value by keeping intermediary instances
	flowInst, err := env.base.CueInst().Fill(env.input.CueInst())
	if err != nil {
		return nil, err
	}
	// Initialize empty output
	out, err := env.cc.EmptyStruct()
	if err != nil {
		return nil, err
	}
	// Cueflow config
	flowCfg := &cueflow.Config{
		UpdateFunc: func(c *cueflow.Controller, t *cueflow.Task) error {
			debugf("compute step")
			if t == nil {
				return nil
			}
			debugf("cueflow task %q: %s", t.Path().String(), t.State().String())
			if t.State() != cueflow.Terminated {
				return nil
			}
			debugf("cueflow task %q: filling result", t.Path().String())
			// Merge task value into output
			var err error
			// FIXME: does cueflow.Task.Value() contain only filled values,
			// or base + filled?
			out, err = out.MergePath(t.Value(), t.Path())
			if err != nil {
				return err
			}
			return nil
		},
	}
	// Cueflow match func
	flowMatchFn := func(v cue.Value) (cueflow.Runner, error) {
		val := env.cc.Wrap(v, flowInst)
		c, err := val.Component()
		if os.IsNotExist(err) {
			// Not a component: skip
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return cueflow.RunnerFunc(func(t *cueflow.Task) error {
			return fn(c, t)
		}), nil
	}
	// Orchestrate execution with cueflow
	flow := cueflow.New(flowCfg, flowInst, flowMatchFn)
	if err := flow.Run(ctx); err != nil {
		return out, err
	}
	return out, nil
}

func envBase(ctx context.Context, c bkgw.Client, cc *Compiler) (*Value, error) {
	// 1. Receive boot script from client.
	debugf("retrieving boot script")
	bootSrc, exists := c.BuildOpts().Opts["boot"]
	if !exists {
		// No boot script: return empty base config
		return cc.EmptyStruct()
	}

	// 2. Compile & execute boot script
	debugf("compiling boot script")
	boot, err := cc.CompileScript("boot.cue", bootSrc)
	if err != nil {
		return nil, errors.Wrap(err, "compile boot script")
	}
	debugf("executing boot script")
	bootState, err := boot.Execute(ctx, NewSolver(c).Scratch(), Discard())
	if err != nil {
		return nil, errors.Wrap(err, "execute boot script")
	}
	// 3. load cue files produced by bootstrap script
	// FIXME: BuildAll() to force all files (no required package..)
	debugf("building cue configuration from boot state")
	base, err := cc.Build(ctx, bootState)
	debugf("done building cue configuration: err=%q", err)
	return base, err
}

func envInput(ctx context.Context, c bkgw.Client, cc *Compiler) (*Value, error) {
	// 1. Receive input overlay from client.
	//     This is used to provide run-time settings, directories..
	inputSrc, exists := c.BuildOpts().Opts["input"]
	if !exists {
		// No input overlay: return empty tree
		return cc.EmptyStruct()
	}
	return cc.Compile("input.cue", inputSrc)
}
