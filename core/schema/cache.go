package schema

import (
	"context"

	"github.com/dagger/dagger/core"
)

type cacheSchema struct {
	*APIServer
}

var _ ExecutableSchema = &cacheSchema{}

func (s *cacheSchema) Name() string {
	return "cache"
}

func (s *cacheSchema) Schema() string {
	return Cache
}

func (s *cacheSchema) Resolvers() Resolvers {
	rs := Resolvers{
		"Query": ObjectResolver{
			"cacheVolume": ToResolver(s.cacheVolume),
		},
	}

	ResolveIDable[core.CacheVolume](rs, "CacheVolume", ObjectResolver{})

	return rs
}

func (s *cacheSchema) Dependencies() []ExecutableSchema {
	return nil
}

type cacheArgs struct {
	Key string
}

func (s *cacheSchema) cacheVolume(ctx context.Context, parent any, args cacheArgs) (*core.CacheVolume, error) {
	// TODO(vito): inject some sort of scope/session/project/user derived value
	// here instead of a static value
	//
	// we have to inject something so we can tell it's a valid ID
	return core.NewCache(args.Key), nil
}
