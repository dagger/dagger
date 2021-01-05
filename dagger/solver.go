package dagger

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
)

// Polyfill for buildkit gateway client
// Use instead of bkgw.Client
type Solver struct {
	c bkgw.Client
}

func NewSolver(c bkgw.Client) Solver {
	return Solver{
		c: c,
	}
}

func (s Solver) FS(input llb.State) FS {
	return FS{
		s:     s,
		input: input,
	}
}

func (s Solver) Scratch() FS {
	return s.FS(llb.Scratch())
}

func (s Solver) Solve(ctx context.Context, st llb.State) (bkgw.Reference, error) {
	// marshal llb
	def, err := st.Marshal(ctx, llb.LinuxAmd64)
	if err != nil {
		return nil, err
	}
	// call solve
	res, err := s.c.Solve(ctx, bkgw.SolveRequest{Definition: def.ToPB()})
	if err != nil {
		return nil, err
	}
	// always use single reference (ignore multiple outputs & metadata)
	return res.SingleRef()
}
