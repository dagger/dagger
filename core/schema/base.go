package schema

import (
	"github.com/dagger/dagger/project"
	"github.com/dagger/dagger/router"
	"github.com/dagger/dagger/sessions"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type InitializeArgs struct {
	Router        *router.Router
	SSHAuthSockID string
	Sessions      *sessions.Manager
	Platform      specs.Platform
	DisableHostRW bool
}

func New(params InitializeArgs) (router.ExecutableSchema, error) {
	base := &baseSchema{
		router:        params.Router,
		sessions:      params.Sessions,
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
		&hostSchema{base},
		&projectSchema{
			baseSchema:    base,
			projectStates: make(map[string]*project.State),
		},
		&httpSchema{base},
		&platformSchema{base},
	)
}

type baseSchema struct {
	router        *router.Router
	sessions      *sessions.Manager
	platform      specs.Platform
	sshAuthSockID string
}
