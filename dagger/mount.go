package dagger

import (
	"context"
	"fmt"

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
		return llb.AddMount(mnt.dest, llb.Scratch(), llb.Tmpfs()), nil
	}
	if err := mnt.Validate("#MountCache"); err == nil {
		// FIXME: cache mount
		return nil, fmt.Errorf("FIXME: cache mount not yet implemented")
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
