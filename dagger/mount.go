package dagger

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

type Mount struct {
	dest string
	v    *Value
}

func newMount(v *Value, dest string) (*Mount, error) {
	if !v.Exists() {
		return nil, ErrNotExist
	}
	return &Mount{
		v:    v,
		dest: dest,
	}, nil
}

func (mnt *Mount) Validate(defs ...string) error {
	return mnt.v.Validate(append(defs, "#Mount")...)
}

func (mnt *Mount) LLB(ctx context.Context, s Solver) (llb.RunOption, error) {
	if err := mnt.Validate("#MountTmp"); err == nil {
		return llb.AddMount(
			mnt.dest,
			llb.Scratch(),
			llb.Tmpfs(),
		), nil
	}
	if err := mnt.Validate("#MountCache"); err == nil {
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

	// possibly construct mount options for LLB from
	var mo []llb.MountOption
	// handle "path" option
	if p := mnt.v.Lookup("path"); p.Exists() {
		ps, err := p.String()
		if err != nil {
			return nil, err
		}
		mo = append(mo, llb.SourcePath(ps))
	}

	return llb.AddMount(mnt.dest, fromFS.LLB(), mo...), nil
}
