package dagger

import (
	"context"
	"encoding/json"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
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

	llb, err := dumpLLB(def)
	if err != nil {
		return nil, err
	}

	log.
		Ctx(ctx).
		Trace().
		RawJSON("llb", llb).
		Msg("solving")

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

type llbOp struct {
	Op         pb.Op
	Digest     digest.Digest
	OpMetadata pb.OpMetadata
}

func dumpLLB(def *llb.Definition) ([]byte, error) {
	ops := make([]llbOp, 0, len(def.Def))
	for _, dt := range def.Def {
		var op pb.Op
		if err := (&op).Unmarshal(dt); err != nil {
			return nil, errors.Wrap(err, "failed to parse op")
		}
		dgst := digest.FromBytes(dt)
		ent := llbOp{Op: op, Digest: dgst, OpMetadata: def.Metadata[dgst]}
		ops = append(ops, ent)
	}
	return json.Marshal(ops)
}
