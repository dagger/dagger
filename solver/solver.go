package solver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	bk "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/dockerfile/dockerfile2llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	bkpb "github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/plancontext"
)

type Solver struct {
	opts     Opts
	eventsWg *sync.WaitGroup
	closeCh  chan *bk.SolveStatus
	refs     []bkgw.Reference
	l        sync.RWMutex

	containers   map[string]*container
	containersMu sync.Mutex
}

type Opts struct {
	Control      *bk.Client
	Gateway      bkgw.Client
	Events       chan *bk.SolveStatus
	Context      *plancontext.Context
	Auth         *RegistryAuthProvider
	NoCache      bool
	CacheImports []bkgw.CacheOptionsEntry
}

func New(opts Opts) *Solver {
	return &Solver{
		eventsWg:   &sync.WaitGroup{},
		closeCh:    make(chan *bk.SolveStatus),
		opts:       opts,
		containers: make(map[string]*container),
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

func (s *Solver) GetOptions() Opts {
	return s.opts
}

func (s *Solver) NoCache() bool {
	return s.opts.NoCache
}

func (s *Solver) Stop(ctx context.Context) {
	lg := log.Ctx(ctx)
	for ctrID := range s.containers {
		if _, err := s.StopContainer(ctx, ctrID); err != nil {
			lg.Error().Err(err).Str("container", ctrID).Msg("failed to stop container")
		}
	}

	close(s.closeCh)
	s.eventsWg.Wait()
	close(s.opts.Events)
}

func (s *Solver) AddCredentials(target, username, secret string) {
	s.opts.Auth.AddCredentials(target, username, secret)
}

func (s *Solver) Marshal(ctx context.Context, st llb.State, co ...llb.ConstraintsOpt) (*bkpb.Definition, error) {
	def, err := st.Marshal(ctx, co...)
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

func (s *Solver) SessionID() string {
	return s.opts.Gateway.BuildOpts().SessionID
}

func (s *Solver) ResolveImageConfig(ctx context.Context, ref string, opts llb.ResolveImageConfigOpt) (dockerfile2llb.Image, digest.Digest, error) {
	var image dockerfile2llb.Image

	// Load image metadata and convert to to LLB.
	// Inspired by https://github.com/moby/buildkit/blob/master/frontend/dockerfile/dockerfile2llb/convert.go
	// FIXME: this needs to handle platform
	dg, meta, err := s.opts.Gateway.ResolveImageConfig(ctx, ref, opts)
	if err != nil {
		return image, "", err
	}
	if err := json.Unmarshal(meta, &image); err != nil {
		return image, "", err
	}

	return image, dg, nil
}

// Solve will block until the state is solved and returns a Reference.
func (s *Solver) SolveRequest(ctx context.Context, req bkgw.SolveRequest) (*bkgw.Result, error) {
	// makes Solve() to block until LLB graph is solved. otherwise it will
	// return result (that you can for example use for next build) that
	// will be evaluated on export or if you access files on it.
	//
	// NOTE: if a future change modifies Evaluate to not always be true anymore, we
	// will need to ensure that Solver.Export no longer sets "ignoreLogs" to true
	// when forwarding progress events. This is because those logs will no longer
	// necessarily be duped from the main channel published to by Solves called here.
	// Sample code to properly handle that can be found here:
	// https://github.com/sipsma/dagger/commit/104e0d0393b5f707ea40448736f2e0e87fb1e4ed
	req.Evaluate = true
	res, err := s.opts.Gateway.Solve(ctx, req)
	if err != nil {
		return nil, CleanError(err)
	}
	return res, nil
}

func (s *Solver) References() []bkgw.Reference {
	s.l.RLock()
	defer s.l.RUnlock()
	return s.refs
}

// Solve will block until the state is solved and returns a Reference.
// It takes a platform as argument which correspond to the targeted platform.
func (s *Solver) Solve(ctx context.Context, st llb.State, platform specs.Platform) (bkgw.Reference, error) {
	def, err := s.Marshal(ctx, st, llb.Platform(platform))
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
		Definition:   def,
		CacheImports: s.opts.CacheImports,
	})
	if err != nil {
		return nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	s.l.Lock()
	defer s.l.Unlock()
	s.refs = append(s.refs, ref)

	return ref, nil
}

// Forward events from solver to the main events channel
// It creates a task in the solver waiting group to be
// sure that everything will be forward to the main channel
func (s *Solver) forwardEvents(ch chan *bk.SolveStatus, ignoreLogs bool) {
	s.eventsWg.Add(1)
	defer s.eventsWg.Done()

	for event := range ch {
		if ignoreLogs {
			event.Logs = nil
		}
		s.opts.Events <- event
	}
}

// Export will export `st` to `output`
// FIXME: this is currently implemented as a hack, starting a new Build session
// within buildkit from the Control API. Ideally the Gateway API should allow to
// Export directly.
func (s *Solver) Export(ctx context.Context, st llb.State, img *dockerfile2llb.Image, output bk.ExportEntry, platform specs.Platform) (*bk.SolveResponse, error) {
	// Check close event channel and return if we're already done with the main pipeline
	select {
	case <-s.closeCh:
		return nil, context.Canceled
	default:
	}

	def, err := s.Marshal(ctx, st, llb.Platform(platform))
	if err != nil {
		return nil, err
	}

	opts := bk.SolveOpt{
		Exports: []bk.ExportEntry{output},
		Session: []session.Attachable{
			s.opts.Auth,
			NewSecretsStoreProvider(s.opts.Context),
			NewDockerSocketProvider(s.opts.Context),
		},
	}

	ch := make(chan *bk.SolveStatus)

	// Forward this build session events to the main events channel, for logging
	// purposes.
	// Ignore logs sent on this channel as they will just be dupes of logs sent
	// to the main progress channel (see #449).
	ignoreLogs := true
	go s.forwardEvents(ch, ignoreLogs)

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
