package dagger

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
	cueload "cuelang.org/go/cue/load"
	cueflow "cuelang.org/go/tools/flow"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"
)

type Solver interface {
	Solve(context.Context, llb.State) (bkgw.Reference, error)
}

// 1 buildkit build = 1 job
type Job struct {
	c bkgw.Client
	// needed for cue operations
	r *Runtime
}

// Execute and wrap the result in a buildkit result
func (job Job) BKExecute(ctx context.Context) (_r *bkgw.Result, _e error) {
	debugf("Executing bk frontend")
	// wrap errors to avoid crashing buildkit with cue error types (why??)
	defer func() {
		if _e != nil {
			_e = fmt.Errorf("%s", cueerrors.Details(_e, nil))
			debugf("execute returned an error. Wrapping...")
		}
	}()
	out, err := job.Execute(ctx)
	if err != nil {
		return nil, err
	}
	// encode job output to buildkit result
	debugf("[runtime] job executed. Encoding output")
	// FIXME: we can't serialize result to standalone cue (with no imports).
	// So the client cannot safely compile output without access to the same cue.mod
	// as the runtime (which we don't want).
	// So for now we return the output as json, still parsed as cue on the client
	// to keep our options open. Once there is a "tree shake" primitive, we can
	// use that to return cue.
	//
	// Uncomment to return actual cue:
	// ----
	// outbytes, err := cueformat.Node(out.Value().Eval().Syntax())
	// if err != nil {
	// 	return nil, err
	// }
	// ----
	outbytes := cueToJSON(out.Value())
	debugf("[runtime] output encoded. Writing output to exporter")
	outref, err := job.Solve(ctx,
		llb.Scratch().File(llb.Mkfile("computed.cue", 0600, outbytes)),
	)
	if err != nil {
		return nil, err
	}
	debugf("[runtime] output written to exporter. returning to buildkit solver")
	res := bkgw.NewResult()
	res.SetRef(outref)
	return res, nil
}

func (job Job) Execute(ctx context.Context) (_i *cue.Instance, _e error) {
	debugf("[runtime] Execute()")
	defer func() { debugf("[runtime] DONE Execute(): err=%v", _e) }()
	state, err := job.Config(ctx)
	if err != nil {
		return nil, err
	}
	// Merge input information into the cue config
	inputs, err := job.Inputs(ctx)
	if err != nil {
		return nil, err
	}
	for target := range inputs {
		// FIXME: cleaner code generation, missing cue.Value.FillPath
		state, err = job.r.fill(state, `#dagger: input: true`, target)
		if err != nil {
			return nil, errors.Wrapf(err, "connect input %q", target)
		}
	}
	action := job.Action()
	switch action {
	case "compute":
		return job.doCompute(ctx, state)
	case "export":
		return job.doExport(ctx, state)
	default:
		return job.doExport(ctx, state)
	}
}

func (job Job) doExport(ctx context.Context, state *cue.Instance) (*cue.Instance, error) {
	return state, nil
}

func (job Job) doCompute(ctx context.Context, state *cue.Instance) (*cue.Instance, error) {
	out, err := job.r.Compile("computed.cue", "")
	if err != nil {
		return nil, err
	}
	// Setup cueflow
	debugf("Setting up cueflow")
	flow := cueflow.New(
		&cueflow.Config{
			UpdateFunc: func(c *cueflow.Controller, t *cueflow.Task) error {
				debugf("cueflow event")
				if t == nil {
					return nil
				}
				debugf("cueflow task %q: %s", t.Path().String(), t.State().String())
				if t.State() == cueflow.Terminated {
					debugf("cueflow task %q: filling result", t.Path().String())
					out, err = out.Fill(t.Value(), cuePathToStrings(t.Path())...)
					if err != nil {
						return err
					}
					// FIXME: catch merge errors early with state
				}
				return nil
			},
		},
		state,
		// Task match func
		func(v cue.Value) (cueflow.Runner, error) {
			// Is v a component (has #dagger) with a field 'compute' ?
			isComponent, err := job.r.isComponent(v, "compute")
			if err != nil {
				return nil, err
			}
			if !isComponent {
				return nil, nil
			}
			debugf("[%s] component detected\n", v.Path().String())
			// task runner func
			runner := cueflow.RunnerFunc(func(t *cueflow.Task) error {
				computeScript := t.Value().LookupPath(cue.ParsePath("#dagger.compute"))
				script, err := job.newScript(computeScript)
				if err != nil {
					return err
				}
				// Run the script & fill the result into the task
				return script.Run(ctx, t)
			})
			return runner, nil
		},
	)
	debugf("Running cueflow")
	if err := flow.Run(ctx); err != nil {
		return nil, err
	}
	debugf("Completed cueflow run. Merging result.")
	state, err = state.Fill(out)
	if err != nil {
		return nil, err
	}
	debugf("Result merged")
	// Return only the computed values
	return out, nil
}

func (job Job) bk() bkgw.Client {
	return job.c
}

func (job Job) Action() string {
	opts := job.bk().BuildOpts().Opts
	if action, ok := opts[bkActionKey]; ok {
		return action
	}
	return ""
}

// Load the cue config for this job
// (received as llb input)
func (job Job) Config(ctx context.Context) (*cue.Instance, error) {
	src := llb.Local(bkConfigKey,
		llb.SessionID(job.bk().BuildOpts().SessionID),
		llb.SharedKeyHint(bkConfigKey),
		llb.WithCustomName("load config"),
	)

	bkInputs, err := job.bk().Inputs(ctx)
	if err != nil {
		return nil, err
	}
	if st, ok := bkInputs[bkConfigKey]; ok {
		src = st
	}
	// job.runDebug(ctx, src, "ls", "-la", "/mnt")
	return job.LoadCue(ctx, src)
}

func (job Job) runDebug(ctx context.Context, mnt llb.State, args ...string) error {
	opts := []llb.RunOption{
		llb.Args(args),
		llb.AddMount("/mnt", mnt),
	}
	cmd := llb.Image("alpine").Run(opts...).Root()
	ref, err := job.Solve(ctx, cmd)
	if err != nil {
		return errors.Wrap(err, "debug")
	}
	// force non-lazy solve
	if _, err := ref.ReadDir(ctx, bkgw.ReadDirRequest{Path: "/"}); err != nil {
		return errors.Wrap(err, "debug")
	}
	return nil
}

func (job Job) Inputs(ctx context.Context) (map[string]llb.State, error) {
	bkInputs, err := job.bk().Inputs(ctx)
	if err != nil {
		return nil, err
	}
	inputs := map[string]llb.State{}
	for key, input := range bkInputs {
		if !strings.HasPrefix(key, bkInputKey) {
			continue
		}
		target := strings.Replace(key, bkInputKey, "", 1)
		targetPath := cue.ParsePath(target)
		if err := targetPath.Err(); err != nil {
			return nil, errors.Wrapf(err, "input target %q", target)
		}
		// FIXME: check that the path can be passed to Fill
		//  (eg. only regular fields, no array indexes, no defs)
		// see cuePathToStrings
		inputs[target] = input
	}
	return inputs, nil
}

// loadFiles recursively loads all .cue files from a buildkit gateway
// FIXME: this is highly inefficient.
func loadFiles(ctx context.Context, ref bkgw.Reference, p, overlayPrefix string, overlay map[string]cueload.Source) error {
	// FIXME: we cannot use `IncludePattern` here, otherwise sub directories
	// (e.g. "cue.mod") will be skipped.
	files, err := ref.ReadDir(ctx, bkgw.ReadDirRequest{
		Path: p,
	})
	if err != nil {
		return err
	}
	for _, f := range files {
		fPath := path.Join(p, f.GetPath())
		if f.IsDir() {
			if err := loadFiles(ctx, ref, fPath, overlayPrefix, overlay); err != nil {
				return err
			}
			continue
		}

		if filepath.Ext(fPath) != ".cue" {
			continue
		}

		contents, err := ref.ReadFile(ctx, bkgw.ReadRequest{
			Filename: fPath,
		})
		if err != nil {
			return errors.Wrap(err, f.GetPath())
		}
		overlay[path.Join(overlayPrefix, fPath)] = cueload.FromBytes(contents)
	}

	return nil
}

func (job Job) LoadCue(ctx context.Context, st llb.State, args ...string) (*cue.Instance, error) {
	// The CUE overlay needs to be prefixed by a non-conflicting path with the
	// local filesystem, otherwise Cue will merge the Overlay with whatever Cue
	// files it finds locally.
	const overlayPrefix = "/config"

	buildConfig := &cueload.Config{
		Dir:     overlayPrefix,
		Overlay: map[string]cueload.Source{},
	}
	buildArgs := args

	// Inject cue files from llb state into overlay
	ref, err := job.Solve(ctx, st)
	if err != nil {
		return nil, err
	}
	if err := loadFiles(ctx, ref, ".", overlayPrefix, buildConfig.Overlay); err != nil {
		return nil, err
	}

	instances := cueload.Instances(buildArgs, buildConfig)
	if len(instances) != 1 {
		return nil, errors.New("only one package is supported at a time")
	}
	inst, err := job.r.Build(instances[0])
	if err != nil {
		return nil, cueErr(err)
	}
	return inst, nil
}

func (job Job) Solve(ctx context.Context, st llb.State) (bkgw.Reference, error) {
	// marshal llb
	def, err := st.Marshal(ctx, llb.LinuxAmd64)
	if err != nil {
		return nil, err
	}
	// call solve
	res, err := job.bk().Solve(ctx, bkgw.SolveRequest{Definition: def.ToPB()})
	if err != nil {
		return nil, err
	}
	// always use single reference (ignore multiple outputs & metadata)
	return res.SingleRef()
}
