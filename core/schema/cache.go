package schema

import (
	"go.dagger.io/dagger/core"
	"go.dagger.io/dagger/router"
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
			"cache": router.ToResolver(s.cache),
		},
		"CacheVolume": router.ObjectResolver{
			"withKey": router.ToResolver(s.withKey),
		},
	}
}

func (s *cacheSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type cacheArgs struct {
	ID core.CacheID
}

func (s *cacheSchema) cache(ctx *router.Context, parent any, args cacheArgs) (*core.CacheVolume, error) {
	if args.ID == "" {
		// TODO(vito): it would make much more sense to have some sort of scope
		// inject an initial value here, but that's not implemented yet.
		return core.NewCache("cache")
	}

	return core.NewCacheFromID(args.ID)
}

type cacheWithKeyArgs struct {
	Key string
}

func (s *cacheSchema) withKey(ctx *router.Context, parent *core.CacheVolume, args cacheWithKeyArgs) (*core.CacheVolume, error) {
	return parent.WithKey(args.Key)
}
