package core

import (
	"context"
	"sync"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/router"
	"github.com/dagger/cloak/secret"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

var (
	registeredSchemas sync.Map
)

type RegisterFunc func(*BaseSchema) router.ExecutableSchema

func Register(name string, fn RegisterFunc) {
	registeredSchemas.Store(name, fn)
}

func New(r *router.Router, secretStore *secret.Store, gw bkgw.Client, platform specs.Platform) (router.ExecutableSchema, error) {
	base := &BaseSchema{
		Router:      r,
		SecretStore: secretStore,
		Gateway:     gw,
		Platform:    platform,
	}
	schemas := []router.ExecutableSchema{}
	registeredSchemas.Range(func(key, value any) bool {
		fn := value.(RegisterFunc)
		schemas = append(schemas, fn(base))
		return true
	})
	return router.Merge(schemas...)
}

type BaseSchema struct {
	Router      *router.Router
	SecretStore *secret.Store
	Gateway     bkgw.Client
	Platform    specs.Platform
}

func (r *BaseSchema) Solve(ctx context.Context, st llb.State, marshalOpts ...llb.ConstraintsOpt) (*filesystem.Filesystem, error) {
	def, err := st.Marshal(ctx, append([]llb.ConstraintsOpt{llb.Platform(r.Platform)}, marshalOpts...)...)
	if err != nil {
		return nil, err
	}
	_, err = r.Gateway.Solve(ctx, bkgw.SolveRequest{
		Evaluate:   true,
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, err
	}

	// FIXME: should we create a filesystem from `res.SingleRef()`?
	return filesystem.FromDefinition(def), nil
}
