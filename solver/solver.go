package solver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	bk "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/dockerfile/dockerfile2llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	bkpb "github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/rs/zerolog/log"
)

type Solver struct {
	opts Opts
}

type Opts struct {
	Control *bk.Client
	Gateway bkgw.Client
	Events  chan *bk.SolveStatus
	Auth    *RegistryAuthProvider
	Secrets session.Attachable
	NoCache bool
}

func New(opts Opts) Solver {
	return Solver{
		opts: opts,
	}
}

func invalidateCache(def *llb.Definition) error {
	for _, dt := range def.Def {
		var op bkpb.Op
		if err := (&op).Unmarshal(dt); err != nil {
			return err
		}
		dgst := digest.FromBytes(dt)
		opMetadata, ok := def.Metadata[dgst]
		if !ok {
			opMetadata = bkpb.OpMetadata{}
		}
		c := llb.Constraints{Metadata: opMetadata}
		llb.IgnoreCache(&c)
		def.Metadata[dgst] = c.Metadata
	}

	return nil
}

func (s Solver) NoCache() bool {
	return s.opts.NoCache
}

func (s Solver) AddCredentials(target, username, secret string) {
	s.opts.Auth.AddCredentials(target, username, secret)
}

func (s Solver) Marshal(ctx context.Context, st llb.State) (*bkpb.Definition, error) {
	// FIXME: do not hardcode the platform
	def, err := st.Marshal(ctx, llb.LinuxAmd64)
	if err != nil {
		return nil, err
	}

	if s.opts.NoCache {
		if err := invalidateCache(def); err != nil {
			return nil, err
		}
	}

	return def.ToPB(), nil
}

func (s Solver) SessionID() string {
	return s.opts.Gateway.BuildOpts().SessionID
}

func (s Solver) ResolveImageConfig(ctx context.Context, ref string, opts llb.ResolveImageConfigOpt) (dockerfile2llb.Image, error) {
	var image dockerfile2llb.Image

	// Load image metadata and convert to to LLB.
	// Inspired by https://github.com/moby/buildkit/blob/master/frontend/dockerfile/dockerfile2llb/convert.go
	// FIXME: this needs to handle platform
	_, meta, err := s.opts.Gateway.ResolveImageConfig(ctx, ref, opts)
	if err != nil {
		return image, err
	}
	if err := json.Unmarshal(meta, &image); err != nil {
		return image, err
	}

	return image, nil
}

// Solve will block until the state is solved and returns a Reference.
func (s Solver) SolveRequest(ctx context.Context, req bkgw.SolveRequest) (*bkgw.Result, error) {
	res, err := s.opts.Gateway.Solve(ctx, req)
	if err != nil {
		return nil, CleanError(err)
	}
	return res, nil
}

// Solve will block until the state is solved and returns a Reference.
func (s Solver) Solve(ctx context.Context, st llb.State) (bkgw.Reference, error) {
	// marshal llb
	def, err := s.Marshal(ctx, st)
	if err != nil {
		return nil, err
	}

	jsonLLB, err := dumpLLB(def)
	if err != nil {
		return nil, err
	}

	log.
		Ctx(ctx).
		Trace().
		RawJSON("llb", jsonLLB).
		Msg("solving")

	// call solve
	res, err := s.SolveRequest(ctx, bkgw.SolveRequest{
		Definition: def,

		// makes Solve() to block until LLB graph is solved. otherwise it will
		// return result (that you can for example use for next build) that
		// will be evaluated on export or if you access files on it.
		Evaluate: true,
	})
	if err != nil {
		return nil, err
	}

	return res.SingleRef()
}

// Export will export `st` to `output`
// FIXME: this is currently impleneted as a hack, starting a new Build session
// within buildkit from the Control API. Ideally the Gateway API should allow to
// Export directly.
func (s Solver) Export(ctx context.Context, st llb.State, img *dockerfile2llb.Image, output bk.ExportEntry) (*bk.SolveResponse, error) {
	def, err := s.Marshal(ctx, st)
	if err != nil {
		return nil, err
	}

	opts := bk.SolveOpt{
		Exports: []bk.ExportEntry{output},
		Session: []session.Attachable{s.opts.Auth, s.opts.Secrets},
	}

	ch := make(chan *bk.SolveStatus)

	// Forward this build session events to the main events channel, for logging
	// purposes.
	go func() {
		for event := range ch {
			s.opts.Events <- event
		}
	}()

	return s.opts.Control.Build(ctx, opts, "", func(ctx context.Context, c bkgw.Client) (*bkgw.Result, error) {
		res, err := c.Solve(ctx, bkgw.SolveRequest{
			Definition: def,
		})
		if err != nil {
			return nil, err
		}

		// Attach the image config if provided
		if img != nil {
			config, err := json.Marshal(img)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal image config: %w", err)
			}

			res.AddMeta(exptypes.ExporterImageConfigKey, config)
		}

		return res, nil
	}, ch)
}

type llbOp struct {
	Op         bkpb.Op
	Digest     digest.Digest
	OpMetadata bkpb.OpMetadata
}

func dumpLLB(def *bkpb.Definition) ([]byte, error) {
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

// A helper to remove noise from buildkit error messages.
// FIXME: Obviously a cleaner solution would be nice.
func CleanError(err error) error {
	noise := []string{
		"executor failed running ",
		"buildkit-runc did not terminate successfully",
		"rpc error: code = Unknown desc = ",
		"failed to solve: ",
	}

	msg := err.Error()

	for _, s := range noise {
		msg = strings.ReplaceAll(msg, s, "")
	}

	return errors.New(msg)
}
