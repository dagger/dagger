package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type cacheSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &cacheSchema{}

func (s *cacheSchema) Name() string {
	return "cache"
}

func (s *cacheSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.NodeFuncWithCacheKey("cacheVolume", s.cacheVolume, s.cacheVolumeCacheKey).
			Doc("Constructs a cache volume for a given cache key.").
			Args(
				dagql.Arg("key").Doc(`A string identifier to target this cache volume (e.g., "modules-cache").`),
				dagql.Arg("namespace"),
			),
	}.Install(s.srv)

	dagql.Fields[*core.CacheVolume]{}.Install(s.srv)
}

func (s *cacheSchema) Dependencies() []SchemaResolvers {
	return nil
}

type cacheArgs struct {
	Key       string
	Namespace string `default:""`
}

func (s *cacheSchema) cacheVolumeCacheKey(ctx context.Context, parent dagql.Instance[*core.Query], args cacheArgs, cacheCfg dagql.CacheConfig) (*dagql.CacheConfig, error) {
	if args.Namespace != "" {
		return &cacheCfg, nil
	}

	/*
			m, err := parent.Self.CurrentModule(ctx)
			if err != nil && !errors.Is(err, core.ErrNoCurrentModule) {
				return nil, err
			}
		namespaceKey := namespaceFromModule(m)
	*/
	namespaceKey := namespaceFromModule(nil)
	cacheCfg.Digest = dagql.HashFrom(cacheCfg.Digest.String(), namespaceKey)
	return &cacheCfg, nil
}

func (s *cacheSchema) cacheVolume(ctx context.Context, parent dagql.Instance[*core.Query], args cacheArgs) (dagql.Instance[*core.CacheVolume], error) {
	var inst dagql.Instance[*core.CacheVolume]

	if args.Namespace != "" {
		return dagql.NewInstanceForCurrentID(ctx, s.srv, parent, core.NewCache(args.Namespace+":"+args.Key))
	}

	/*
		m, err := parent.Self.CurrentModule(ctx)
		if err != nil && !errors.Is(err, core.ErrNoCurrentModule) {
			return inst, err
		}
		namespaceKey := namespaceFromModule(m)
	*/
	var err error
	namespaceKey := namespaceFromModule(nil)
	err = s.srv.Select(ctx, s.srv.Root(), &inst, dagql.Selector{
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

// func namespaceFromModule(m *core.Module) string {
func namespaceFromModule(m any) string {
	return "mainClient"
	/*
		if m == nil {
			return "mainClient"
		}

		name := m.Source.Self.ModuleOriginalName

		var symbolic string
		switch m.Source.Self.Kind {
		case core.ModuleSourceKindLocal:
			symbolic = m.Source.Self.SourceRootSubpath
		case core.ModuleSourceKindGit:
			symbolic = m.Source.Self.Git.Symbolic
		case core.ModuleSourceKindDir:
			symbolic = m.Source.ID().Digest().String()
		}

		return "mod(" + name + symbolic + ")"
	*/
}
