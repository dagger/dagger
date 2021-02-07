package dagger

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"

	"dagger.cloud/go/dagger/cc"
)

type Mount struct {
	dest string
	v    *cc.Value
}

func newMount(v *cc.Value, dest string) (*Mount, error) {
	if !v.Exists() {
		return nil, ErrNotExist
	}
	return &Mount{
		v:    v,
		dest: dest,
	}, nil
}

func (mnt *Mount) LLB(ctx context.Context, s Solver) (llb.RunOption, error) {
	if err := spec.Validate(mnt.v, "#MountTmp"); err == nil {
		return llb.AddMount(
			mnt.dest,
			llb.Scratch(),
			llb.Tmpfs(),
		), nil
	}
	if err := spec.Validate(mnt.v, "#MountCache"); err == nil {
		return llb.AddMount(
			mnt.dest,
			llb.Scratch(),
			llb.AsPersistentCacheDir(
				mnt.v.Path().String(),
				llb.CacheMountShared,
			)), nil
	}
	// Compute source component or script, discarding fs writes & output value
	from, err := newExecutable(mnt.v.Lookup("from"))
	if err != nil {
		return nil, errors.Wrap(err, "from")
	}
	fromFS, err := from.Execute(ctx, s.Scratch(), nil)
	if err != nil {
		return nil, err
	}
	return llb.AddMount(mnt.dest, fromFS.LLB()), nil
}
