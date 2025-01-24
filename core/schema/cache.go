package schema

import (
	"context"
	"errors"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/opencontainers/go-digest"
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
			ArgDoc("key", `A string identifier to target this cache volume (e.g., "modules-cache").`),
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

func (s *cacheSchema) cacheVolumeCacheKey(ctx context.Context, parent dagql.Instance[*core.Query], args cacheArgs, origDgst digest.Digest) (digest.Digest, error) {
	if args.Namespace != "" {
		return origDgst, nil
	}

	m, err := parent.Self.CurrentModule(ctx)
	if err != nil && !errors.Is(err, core.ErrNoCurrentModule) {
		return "", err
	}
	namespaceKey, err := namespaceFromModule(ctx, m)
	if err != nil {
		return "", err
	}

	return hashFrom(origDgst.String(), namespaceKey), nil
}

func (s *cacheSchema) cacheVolume(ctx context.Context, parent dagql.Instance[*core.Query], args cacheArgs) (dagql.Instance[*core.CacheVolume], error) {
	var inst dagql.Instance[*core.CacheVolume]

	if args.Namespace != "" {
		return dagql.NewInstanceForCurrentID(ctx, s.srv, parent, core.NewCache(args.Namespace+":"+args.Key))
	}

	m, err := parent.Self.CurrentModule(ctx)
	if err != nil && !errors.Is(err, core.ErrNoCurrentModule) {
		return inst, err
	}
	namespaceKey, err := namespaceFromModule(ctx, m)
	if err != nil {
		return inst, err
	}

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

func namespaceFromModule(ctx context.Context, m *core.Module) (string, error) {
	if m == nil {
		return "mainClient", nil
	}

	name, err := m.Source.Self.ModuleName(ctx)
	if err != nil {
		return "", err
	}

	symbolic, err := m.Source.Self.Symbolic()
	if err != nil {
		return "", err
	}

	return "mod(" + name + symbolic + ")", nil
}
