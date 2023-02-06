package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/project"
	"github.com/dagger/dagger/router"
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
}

func New(params InitializeArgs) (router.ExecutableSchema, error) {
	base := &baseSchema{
		router:    params.Router,
		gw:        params.Gateway,
		bkClient:  params.BKClient,
		solveOpts: params.SolveOpts,
		solveCh:   params.SolveCh,
		platform:  params.Platform,
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
}
