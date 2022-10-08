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
			"cache":           router.ToResolver(s.cache),
			"cacheFromTokens": router.ToResolver(s.cacheFromTokens),
		},
		"CacheVolume": router.ObjectResolver{},
	}
}

func (s *cacheSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type cacheArgs struct {
	ID core.CacheID
}

func (s *cacheSchema) cache(ctx *router.Context, parent any, args cacheArgs) (*core.Cache, error) {
	return core.NewCacheFromID(args.ID)
}

type cacheFromTokensArgs struct {
	Tokens []string
}

func (s *cacheSchema) cacheFromTokens(ctx *router.Context, parent any, args cacheFromTokensArgs) (*core.Cache, error) {
	return core.NewCache(args.Tokens)
}
