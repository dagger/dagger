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
	SSHAuthSockID string
	WorkdirID     core.HostDirectoryID
	Gateway       bkgw.Client
	BKClient      *bkclient.Client
	SolveOpts     bkclient.SolveOpt
	SolveCh       chan *bkclient.SolveStatus
	Platform      specs.Platform
}

func New(params InitializeArgs) (router.ExecutableSchema, error) {
	base := &baseSchema{
		router:        params.Router,
		gw:            params.Gateway,
		bkClient:      params.BKClient,
		solveOpts:     params.SolveOpts,
		solveCh:       params.SolveCh,
		platform:      params.Platform,
		sshAuthSockID: params.SSHAuthSockID,
	}
	return router.MergeExecutableSchemas("core",
		&directorySchema{base},
		&fileSchema{base},
		&gitSchema{base},
		&containerSchema{base},
		&cacheSchema{base},
		&secretSchema{base},
		&hostSchema{base, params.WorkdirID},
		&projectSchema{
			baseSchema:    base,
			projectStates: make(map[string]*project.State),
		},
		&httpSchema{base},
	)
}

type baseSchema struct {
	router        *router.Router
	gw            bkgw.Client
	bkClient      *bkclient.Client
	solveOpts     bkclient.SolveOpt
	solveCh       chan *bkclient.SolveStatus
	platform      specs.Platform
	sshAuthSockID string
}
