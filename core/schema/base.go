package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/project"
	"github.com/dagger/dagger/router"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type InitializeArgs struct {
	Router        *router.Router
	SSHAuthSockID string
	WorkdirPath   string
	Session       *core.Session
	Platform      specs.Platform
}

func New(params InitializeArgs) (router.ExecutableSchema, error) {
	base := &baseSchema{
		router:        params.Router,
		rootSession:   params.Session,
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
		&hostSchema{base, core.NewHost(params.WorkdirPath)},
		&projectSchema{
			baseSchema:    base,
			projectStates: make(map[string]*project.State),
		},
		&httpSchema{base},
	)
}

type baseSchema struct {
	router        *router.Router
	rootSession   *core.Session
	platform      specs.Platform
	sshAuthSockID string
}
