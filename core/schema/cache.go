package schema

import (
	"context"
	"errors"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/hashutil"
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

func (s *cacheSchema) cacheVolumeCacheKey(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Query],
	args cacheArgs,
	req dagql.GetCacheConfigRequest,
) (*dagql.GetCacheConfigResponse, error) {
	resp := &dagql.GetCacheConfigResponse{CacheKey: req.CacheKey}
	if args.Namespace != "" {
		return resp, nil
	}

	m, err := parent.Self().CurrentModule(ctx)
	if err != nil && !errors.Is(err, core.ErrNoCurrentModule) {
		return nil, err
	}
	namespaceKey := namespaceFromModule(m)
	resp.CacheKey.CallKey = hashutil.HashStrings(resp.CacheKey.CallKey, namespaceKey).String()
	return resp, nil
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
	namespaceKey := namespaceFromModule(m)
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

	src := m.Source.Value
	name := src.Self().ModuleOriginalName

	var symbolic string
	switch src.Self().Kind {
	case core.ModuleSourceKindLocal:
		symbolic = src.Self().SourceRootSubpath
	case core.ModuleSourceKindGit:
		symbolic = src.Self().Git.Symbolic
	case core.ModuleSourceKindDir:
		symbolic = m.Source.Value.ID().Digest().String()
	}

	return "mod(" + name + symbolic + ")"
}
