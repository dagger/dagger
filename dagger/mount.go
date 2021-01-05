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
	from, err := mnt.v.Lookup("from").Executable()
	if err != nil {
		return nil, errors.Wrap(err, "from")
	}
	fromFS, err := from.Execute(ctx, s.Scratch(), Discard())
	if err != nil {
		return nil, err
	}
	return llb.AddMount(mnt.dest, fromFS.LLB()), nil
}
