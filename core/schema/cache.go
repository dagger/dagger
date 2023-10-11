package schema

import (
	"github.com/dagger/dagger/core"
)

type cacheSchema struct {
	*MergedSchemas
}

var _ ExecutableSchema = &cacheSchema{}

func (s *cacheSchema) Name() string {
	return "cache"
}

func (s *cacheSchema) Schema() string {
	return Cache
}

var cacheIDResolver = stringResolver(core.CacheID(""))

func (s *cacheSchema) Resolvers() Resolvers {
	return Resolvers{
		"CacheID": cacheIDResolver,
		"Query": ObjectResolver{
			"cacheVolume": ToResolver(s.cacheVolume),
		},
		"CacheVolume": ToIDableObjectResolver(core.CacheID.Decode, ObjectResolver{
			"id": ToResolver(s.id),
		}),
	}
}

func (s *cacheSchema) Dependencies() []ExecutableSchema {
	return nil
}

func (s *cacheSchema) id(ctx *core.Context, parent *core.CacheVolume, args any) (core.CacheID, error) {
	return parent.ID()
}

type cacheArgs struct {
	Key string
}

func (s *cacheSchema) cacheVolume(ctx *core.Context, parent any, args cacheArgs) (*core.CacheVolume, error) {
	// TODO(vito): inject some sort of scope/session/project/user derived value
	// here instead of a static value
	//
	// we have to inject something so we can tell it's a valid ID
	return core.NewCache(args.Key), nil
}
