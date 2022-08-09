package core

import (
	"context"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/router"
	"github.com/dagger/cloak/secret"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func New(r *router.Router, secretStore *secret.Store, gw bkgw.Client, platform specs.Platform) []router.ExecutableSchema {
	base := &baseSchema{
		router:      r,
		secretStore: secretStore,
		gw:          gw,
		platform:    platform,
	}
	return []router.ExecutableSchema{
		&coreSchema{base},

		&filesystemSchema{base},
		&extensionSchema{base},
		&execSchema{base},
		&dockerBuildSchema{base},

		&secretSchema{base},
	}
}

type baseSchema struct {
	router      *router.Router
	secretStore *secret.Store
	gw          bkgw.Client
	platform    specs.Platform
}

func (r *baseSchema) Solve(ctx context.Context, st llb.State) (*filesystem.Filesystem, error) {
	def, err := st.Marshal(ctx, llb.Platform(r.platform))
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
