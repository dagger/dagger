package schema

import (
	"github.com/containerd/containerd/content"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
	"github.com/dagger/dagger/secret"
	bkclient "github.com/moby/buildkit/client"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type InitializeArgs struct {
	Router             *router.Router
	Workdir            string
	Gateway            *core.GatewayClient
	BKClient           *bkclient.Client
	SolveOpts          bkclient.SolveOpt
	SolveCh            chan *bkclient.SolveStatus
	OCIStore           content.Store
	Platform           specs.Platform
	DisableHostRW      bool
	Auth               *auth.RegistryAuthProvider
	Secrets            *secret.Store
	ProgrockSocket     string
	ExtraSearchDomains []string

	// TODO(vito): remove when stable
	EnableServices bool
}

func New(params InitializeArgs) (router.ExecutableSchema, error) {
	base := &baseSchema{
		router:             params.Router,
		gw:                 params.Gateway,
		bkClient:           params.BKClient,
		solveOpts:          params.SolveOpts,
		solveCh:            params.SolveCh,
		platform:           params.Platform,
		auth:               params.Auth,
		secrets:            params.Secrets,
		progSock:           params.ProgrockSocket,
		extraSearchDomains: params.ExtraSearchDomains,

		// TODO(vito): remove when stable
		servicesEnabled: params.EnableServices,
	}
	host := core.NewHost(params.Workdir, params.DisableHostRW)
	return router.MergeExecutableSchemas("core",
		&querySchema{base},
		&directorySchema{base, host},
		&fileSchema{base, host},
		&gitSchema{base},
		&containerSchema{base, host, params.OCIStore},
		&cacheSchema{base},
		&secretSchema{base},
		&hostSchema{base, host},
		&projectSchema{base},
		&httpSchema{base},
		&platformSchema{base},
		&socketSchema{base, host},
		&serviceSchema{base},
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

	// path to Progrock forwarding socket
	progSock string

	// search domains to use for DNS resolution
	extraSearchDomains []string
}

func (s *baseSchema) searchDomains() []string {
	return append([]string{core.ServicesDomain()}, s.extraSearchDomains...)
}
