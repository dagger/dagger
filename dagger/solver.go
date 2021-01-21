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

// Solve will block until the state is solved and returns a Reference.
func (s Solver) Solve(ctx context.Context, st llb.State) (bkgw.Reference, error) {
	// marshal llb
	def, err := st.Marshal(ctx, llb.LinuxAmd64)
	if err != nil {
		return nil, err
	}
	// call solve
	res, err := s.c.Solve(ctx, bkgw.SolveRequest{
		Definition: def.ToPB(),

		// makes Solve() to block until LLB graph is solved. otherwise it will
		// return result (that you can for example use for next build) that
		// will be evaluated on export or if you access files on it.
		Evaluate: true,
	})
	if err != nil {
		return nil, bkCleanError(err)
	}
	// always use single reference (ignore multiple outputs & metadata)
	return res.SingleRef()
}
