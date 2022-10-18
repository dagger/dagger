package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
)

type cacheSchema struct {
	*baseSchema
}

var _ router.ExecutableSchema = &cacheSchema{}

func (s *cacheSchema) Name() string {
	return "cache"
}

func (s *cacheSchema) Schema() string {
	return Cache
}

var cacheIDResolver = stringResolver(core.CacheID(""))

func (s *cacheSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"CacheID": cacheIDResolver,
		"Query": router.ObjectResolver{
			"cacheVolume": router.ToResolver(s.cacheVolume),
		},
		"CacheVolume": router.ObjectResolver{},
	}
}

func (s *cacheSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type cacheArgs struct {
	Key string
}

func (s *cacheSchema) cacheVolume(ctx *router.Context, parent any, args cacheArgs) (*core.CacheVolume, error) {
	// TODO(vito): inject some sort of scope/session/project/user derived value
	// here instead of a static value
	//
	// we have to inject something so we can tell it's a valid ID
	return core.NewCache(args.Key)
}
