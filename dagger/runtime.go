//go:generate sh gen.sh
package dagger

import (
	"context"
	"fmt"
	"sync"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"
)

type Runtime struct {
	l sync.Mutex

	cue.Runtime
}

func (r *Runtime) Cue() *cue.Runtime {
	return &(r.Runtime)
}

func (r *Runtime) fill(inst *cue.Instance, v interface{}, target string) (*cue.Instance, error) {
	targetPath := cue.ParsePath(target)
	if err := targetPath.Err(); err != nil {
		return nil, err
	}
	p := cuePathToStrings(targetPath)
	if src, ok := v.(string); ok {
		vinst, err := r.Compile(target, src)
		if err != nil {
			return nil, err
		}
		return inst.Fill(vinst.Value(), p...)
	}
	return inst.Fill(v, p...)
}

// func (r Runtime) Run(...)
// Buildkit run entrypoint
func (r *Runtime) BKFrontend(ctx context.Context, c bkgw.Client) (*bkgw.Result, error) {
	return r.newJob(c).BKExecute(ctx)
}

func (r *Runtime) newJob(c bkgw.Client) Job {
	return Job{
		r: r,
		c: c,
	}
}

// Check whether a value is a valid component
// FIXME: calling matchSpec("#Component") is not enough because
//   it does not match embedded scalars.
func (r *Runtime) isComponent(v cue.Value, fields ...string) (bool, error) {
	cfg := v.LookupPath(cue.ParsePath("#dagger"))
	if cfg.Err() != nil {
		// No "#dagger" -> not a component
		return false, nil
	}
	for _, field := range fields {
		if cfg.Lookup(field).Err() != nil {
			return false, nil
		}
	}
	if err := r.validateSpec(cfg, "#ComponentConfig"); err != nil {
		return true, errors.Wrap(err, "invalid #dagger")
	}
	return true, nil
}

// eg. validateSpec(op, "#Op")
// eg. validateSpec(dag, "#DAG")
func (r *Runtime) validateSpec(v cue.Value, defpath string) (err error) {
	// Expand cue errors to get full details
	// FIXME: there is probably a cleaner way to do this.
	defer func() {
		if err != nil {
			err = fmt.Errorf("%s", cueerrors.Details(err, nil))
		}
	}()
	r.l.Lock()
	defer r.l.Unlock()

	// FIXME cache spec instance
	spec, err := r.Compile("dagger.cue", DaggerSpec)
	if err != nil {
		panic("invalid spec")
	}
	def := spec.Value().LookupPath(cue.ParsePath(defpath))
	if err := def.Err(); err != nil {
		return err
	}
	v = v.Eval()
	if err := v.Validate(); err != nil {
		return err
	}
	res := def.Unify(v)
	if err := res.Validate(cue.Final()); err != nil {
		return err
	}
	return nil
}

func (r *Runtime) matchSpec(v cue.Value, def string) bool {
	return r.validateSpec(v, def) == nil
}
