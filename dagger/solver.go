package dagger

import (
	"context"
	"encoding/json"
	"fmt"

	bk "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/dockerfile2llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	bkpb "github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/rs/zerolog/log"
)

// Polyfill for buildkit gateway client
// Use instead of bkgw.Client
type Solver struct {
	events  chan *bk.SolveStatus
	control *bk.Client
	gw      bkgw.Client
}

func NewSolver(control *bk.Client, gw bkgw.Client, events chan *bk.SolveStatus) Solver {
	return Solver{
		events:  events,
		control: control,
		gw:      gw,
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

func (s Solver) SessionID() string {
	return s.gw.BuildOpts().SessionID
}

func (s Solver) ResolveImageConfig(ctx context.Context, ref string, opts llb.ResolveImageConfigOpt) (dockerfile2llb.Image, error) {
	var image dockerfile2llb.Image

	// Load image metadata and convert to to LLB.
	// Inspired by https://github.com/moby/buildkit/blob/master/frontend/dockerfile/dockerfile2llb/convert.go
	// FIXME: this needs to handle platform
	_, meta, err := s.gw.ResolveImageConfig(ctx, ref, opts)
	if err != nil {
		return image, err
	}
	if err := json.Unmarshal(meta, &image); err != nil {
		return image, err
	}

	return image, nil
}

// Solve will block until the state is solved and returns a Reference.
func (s Solver) SolveRequest(ctx context.Context, req bkgw.SolveRequest) (bkgw.Reference, error) {
	// call solve
	res, err := s.gw.Solve(ctx, req)
	if err != nil {
		return nil, bkCleanError(err)
	}
	// always use single reference (ignore multiple outputs & metadata)
	return res.SingleRef()
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
	return s.SolveRequest(ctx, bkgw.SolveRequest{
		Definition: def.ToPB(),

		// makes Solve() to block until LLB graph is solved. otherwise it will
		// return result (that you can for example use for next build) that
		// will be evaluated on export or if you access files on it.
		Evaluate: true,
	})
}

// Export will export `st` to `output`
// FIXME: this is currently impleneted as a hack, starting a new Build session
// within buildkit from the Control API. Ideally the Gateway API should allow to
// Export directly.
func (s Solver) Export(ctx context.Context, st llb.State, output bk.ExportEntry) (*bk.SolveResponse, error) {
	def, err := st.Marshal(ctx, llb.LinuxAmd64)
	if err != nil {
		return nil, err
	}

	opts := bk.SolveOpt{
		Exports: []bk.ExportEntry{output},
		Session: []session.Attachable{
			authprovider.NewDockerAuthProvider(log.Ctx(ctx)),
		},
	}

	ch := make(chan *bk.SolveStatus)

	go func() {
		for event := range ch {
			s.events <- event
		}
	}()

	return s.control.Build(ctx, opts, "", func(ctx context.Context, c bkgw.Client) (*bkgw.Result, error) {
		return c.Solve(ctx, bkgw.SolveRequest{
			Definition: def.ToPB(),
		})
	}, ch)
}

type llbOp struct {
	Op         bkpb.Op
	Digest     digest.Digest
	OpMetadata bkpb.OpMetadata
}

func dumpLLB(def *llb.Definition) ([]byte, error) {
	ops := make([]llbOp, 0, len(def.Def))
	for _, dt := range def.Def {
		var op bkpb.Op
		if err := (&op).Unmarshal(dt); err != nil {
			return nil, fmt.Errorf("failed to parse op: %w", err)
		}
		dgst := digest.FromBytes(dt)
		ent := llbOp{Op: op, Digest: dgst, OpMetadata: def.Metadata[dgst]}
		ops = append(ops, ent)
	}
	return json.Marshal(ops)
}
