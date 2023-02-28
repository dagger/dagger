package schema

import (
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/project"
	"github.com/dagger/dagger/router"
	"github.com/dagger/dagger/secret"
	bkclient "github.com/moby/buildkit/client"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type InitializeArgs struct {
	Router        *router.Router
	Workdir       string
	Gateway       *core.GatewayClient
	BKClient      *bkclient.Client
	SolveOpts     bkclient.SolveOpt
	SolveCh       chan *bkclient.SolveStatus
	Platform      specs.Platform
	DisableHostRW bool
	Auth          *auth.RegistryAuthProvider
	Secrets       *secret.Store

	// TODO(vito): remove when stable
	EnableServices bool
}

func New(params InitializeArgs) (router.ExecutableSchema, error) {
	base := &baseSchema{
		router:    params.Router,
		gw:        params.Gateway,
		bkClient:  params.BKClient,
		solveOpts: params.SolveOpts,
		solveCh:   params.SolveCh,
		platform:  params.Platform,
		auth:      params.Auth,
		secrets:   params.Secrets,

		// TODO(vito): remove when stable
		servicesEnabled: params.EnableServices,
	}
	host := core.NewHost(params.Workdir, params.DisableHostRW)
	return router.MergeExecutableSchemas("core",
		&querySchema{base},
		&directorySchema{base, host},
		&fileSchema{base, host},
		&gitSchema{base},
		&containerSchema{base, host},
		&cacheSchema{base},
		&secretSchema{base},
		&hostSchema{base, host},
		&projectSchema{
			baseSchema:    base,
			projectStates: make(map[string]*project.State),
		},
		&httpSchema{base},
		&platformSchema{base},
		&socketSchema{base, host},
	)
}

type baseSchema struct {
	router    *router.Router
	gw        bkgw.Client
	bkClient  *bkclient.Client
	solveOpts bkclient.SolveOpt
	solveCh   chan *bkclient.SolveStatus
	platform  specs.Platform
	auth      *auth.RegistryAuthProvider
	secrets   *secret.Store

	// TODO(vito): remove when stable
	servicesEnabled bool
}
