package core

import (
	"context"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/core/service"
	"github.com/dagger/cloak/project"
	"github.com/dagger/cloak/router"
	"github.com/dagger/cloak/secret"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type InitializeArgs struct {
	Router        *router.Router
	SecretStore   *secret.Store
	SSHAuthSockID string
	WorkdirID     string
	Gateway       bkgw.Client
	BKClient      *bkclient.Client
	SolveOpts     bkclient.SolveOpt
	SolveCh       chan *bkclient.SolveStatus
	Platform      specs.Platform
}

func New(params InitializeArgs) (router.ExecutableSchema, error) {
	base := &baseSchema{
		router:      params.Router,
		secretStore: params.SecretStore,
		gw:          params.Gateway,
		bkClient:    params.BKClient,
		solveOpts:   params.SolveOpts,
		solveCh:     params.SolveCh,
		platform:    params.Platform,
	}
	return router.MergeExecutableSchemas("core",
		&coreSchema{base, params.SSHAuthSockID, params.WorkdirID},

		&filesystemSchema{base},
		&projectSchema{
			baseSchema:      base,
			compiledSchemas: make(map[string]*project.CompiledRemoteSchema),
			sshAuthSockID:   params.SSHAuthSockID,
		},
		&execSchema{base, params.SSHAuthSockID},
		&dockerBuildSchema{base},
		&serviceSchema{
			baseSchema: base,
			svcs:       make(map[string]*service.Service),
		},

		&secretSchema{base},
	)
}

type baseSchema struct {
	router      *router.Router
	secretStore *secret.Store
	gw          bkgw.Client
	bkClient    *bkclient.Client
	solveOpts   bkclient.SolveOpt
	solveCh     chan *bkclient.SolveStatus
	platform    specs.Platform
}

func (r *baseSchema) Solve(ctx context.Context, st llb.State, marshalOpts ...llb.ConstraintsOpt) (*filesystem.Filesystem, error) {
	def, err := st.Marshal(ctx, append([]llb.ConstraintsOpt{llb.Platform(r.platform)}, marshalOpts...)...)
	if err != nil {
		return nil, err
	}
	_, err = r.gw.Solve(ctx, bkgw.SolveRequest{
		Evaluate:   true,
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, err
	}

	// FIXME: should we create a filesystem from `res.SingleRef()`?
	return filesystem.FromDefinition(def), nil
}

func (r *baseSchema) Export(ctx context.Context, fs *filesystem.Filesystem, export bkclient.ExportEntry) error {
	fsDef, err := fs.ToDefinition()
	if err != nil {
		return err
	}

	solveOpts := r.solveOpts
	// NOTE: be careful to not overwrite any values from original shared r.solveOpts (i.e. with append).
	solveOpts.Exports = []bkclient.ExportEntry{export}

	// Mirror events from the sub-Build into the main Build event channel.
	// Build() will close the channel after completion so we don't want to use the main channel directly.
	ch := make(chan *bkclient.SolveStatus)
	go func() {
		for event := range ch {
			r.solveCh <- event
		}
	}()

	_, err = r.bkClient.Build(ctx, solveOpts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		return gw.Solve(ctx, bkgw.SolveRequest{
			Evaluate:   true,
			Definition: fsDef,
		})
	}, ch)
	if err != nil {
		return err
	}
	return nil
}
