package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

type engineSchema struct{}

var _ SchemaResolvers = &engineSchema{}

func (s *engineSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("engine", s.engine).
			Doc("The Dagger engine container configuration and state"),
	}.Install(srv)

	dagql.Fields[*core.Engine]{
		dagql.Func("clients", s.clients).
			Doc("The list of connected client IDs"),
	}.Install(srv)

	dagql.Fields[*core.Engine]{
		dagql.Func("localCache", s.localCache).
			Doc("The local (on-disk) cache for the Dagger engine"),
	}.Install(srv)

	dagql.Fields[*core.EngineCache]{
		dagql.NodeFuncWithCacheKey("entrySet", s.cacheEntrySet, dagql.CachePerCall).
			Doc("The current set of entries in the cache"),
		dagql.Func("prune", s.cachePrune).
			DoNotCache("Mutates mutable state").
			Doc("Prune the cache of releaseable entries").
			Args(
				dagql.Arg("useDefaultPolicy").Doc("Use the engine-wide default pruning policy if true, otherwise prune the whole cache of any releasable entries."),
			),
	}.Install(srv)

	dagql.Fields[*core.EngineCacheEntrySet]{
		dagql.Func("entries", s.cacheEntrySetEntries).
			Doc("The list of individual cache entries in the set"),
	}.Install(srv)

	dagql.Fields[*core.EngineCacheEntry]{}.Install(srv)
}

func (s *engineSchema) engine(ctx context.Context, parent *core.Query, args struct{}) (*core.Engine, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	return &core.Engine{
		Name: query.EngineName(),
	}, nil
}

func (s *engineSchema) localCache(ctx context.Context, parent *core.Engine, args struct{}) (*core.EngineCache, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	if err := query.RequireMainClient(ctx); err != nil {
		return nil, err
	}
	policy := query.Clone().EngineLocalCachePolicy()
	if policy == nil {
		return &core.EngineCache{}, nil
	}
	return &core.EngineCache{
		ReservedSpace: int(policy.ReservedSpace),
		TargetSpace:   int(policy.TargetSpace),
		MaxUsedSpace:  int(policy.MaxUsedSpace),
		MinFreeSpace:  int(policy.MinFreeSpace),
	}, nil
}

func (s *engineSchema) clients(ctx context.Context, parent *core.Engine, args struct{}) ([]string, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	return query.Clients(), nil
}

func (s *engineSchema) cacheEntrySet(ctx context.Context, parent dagql.ObjectResult[*core.EngineCache], args struct {
	Key string `default:""`
}) (inst dagql.Result[*core.EngineCacheEntrySet], _ error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	if err := query.RequireMainClient(ctx); err != nil {
		return inst, err
	}
	srv, err := query.Server.Server(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get server: %w", err)
	}

	if args.Key == "" {
		err := srv.Select(ctx, parent, &inst,
			dagql.Selector{
				Field: "entrySet",
				Args: []dagql.NamedInput{
					{
						Name:  "key",
						Value: dagql.NewString(identity.NewID()),
					},
				},
			},
		)
		return inst, err
	}

	entrySet, err := query.EngineLocalCacheEntries(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to load cache entries: %w", err)
	}

	return dagql.NewResultForCurrentID(ctx, entrySet)
}

func (s *engineSchema) cachePrune(ctx context.Context, parent *core.EngineCache, args struct {
	UseDefaultPolicy bool `default:"false"`
}) (dagql.Nullable[core.Void], error) {
	void := dagql.Null[core.Void]()
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return void, err
	}
	if err := query.RequireMainClient(ctx); err != nil {
		return void, err
	}

	_, err = query.PruneEngineLocalCacheEntries(ctx, args.UseDefaultPolicy)
	if err != nil {
		return void, fmt.Errorf("failed to prune cache entries: %w", err)
	}

	return void, nil
}

func (s *engineSchema) cacheEntrySetEntries(ctx context.Context, parent *core.EngineCacheEntrySet, args struct{}) (dagql.Array[*core.EngineCacheEntry], error) {
	return parent.EntriesList, nil
}
