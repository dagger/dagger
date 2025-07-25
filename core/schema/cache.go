package schema

import (
	"context"
	"errors"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type cacheSchema struct{}

var _ SchemaResolvers = &cacheSchema{}

func (s *cacheSchema) Name() string {
	return "cache"
}

func (s *cacheSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.NodeFuncWithCacheKey("cacheVolume", s.cacheVolume, s.cacheVolumeCacheKey).
			Doc("Constructs a cache volume for a given cache key.").
			Args(
				dagql.Arg("key").Doc(`A string identifier to target this cache volume (e.g., "modules-cache").`),
			),
	}.Install(srv)

	dagql.Fields[*core.CacheVolume]{}.Install(srv)
}

func (s *cacheSchema) Dependencies() []SchemaResolvers {
	return nil
}

type cacheArgs struct {
	Key       string
	Namespace string `internal:"true" default:""`
}

func (s *cacheSchema) cacheVolumeCacheKey(ctx context.Context, parent dagql.ObjectResult[*core.Query], args cacheArgs, cacheCfg dagql.CacheConfig) (*dagql.CacheConfig, error) {
	if args.Namespace != "" {
		return &cacheCfg, nil
	}

	m, err := parent.Self().CurrentModule(ctx)
	if err != nil && !errors.Is(err, core.ErrNoCurrentModule) {
		return nil, err
	}
	namespaceKey := namespaceFromModule(m.Self())
	cacheCfg.Digest = dagql.HashFrom(cacheCfg.Digest.String(), namespaceKey)
	return &cacheCfg, nil
}

func (s *cacheSchema) cacheVolume(ctx context.Context, parent dagql.ObjectResult[*core.Query], args cacheArgs) (dagql.Result[*core.CacheVolume], error) {
	var inst dagql.Result[*core.CacheVolume]

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	if args.Namespace != "" {
		return dagql.NewResultForCurrentID(ctx, core.NewCache(args.Namespace+":"+args.Key))
	}

	m, err := parent.Self().CurrentModule(ctx)
	if err != nil && !errors.Is(err, core.ErrNoCurrentModule) {
		return inst, err
	}
	namespaceKey := namespaceFromModule(m.Self())
	err = srv.Select(ctx, srv.Root(), &inst, dagql.Selector{
		Field: "cacheVolume",
		Args: []dagql.NamedInput{
			{
				Name:  "key",
				Value: dagql.NewString(args.Key),
			},
			{
				Name:  "namespace",
				Value: dagql.NewString(namespaceKey),
			},
		},
	})
	if err != nil {
		return inst, err
	}

	return inst, nil
}

func namespaceFromModule(m *core.Module) string {
	if m == nil {
		return "mainClient"
	}

	name := m.Source.Self().ModuleOriginalName

	var symbolic string
	switch m.Source.Self().Kind {
	case core.ModuleSourceKindLocal:
		symbolic = m.Source.Self().SourceRootSubpath
	case core.ModuleSourceKindGit:
		symbolic = m.Source.Self().Git.Symbolic
	case core.ModuleSourceKindDir:
		symbolic = m.Source.ID().Digest().String()
	}

	return "mod(" + name + symbolic + ")"
}
