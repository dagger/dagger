package schema

import (
	"context"
	"errors"

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
		dagql.NodeFunc("cacheVolume", s.cacheVolume).
			Doc("Constructs a cache volume for a given cache key.").
			Impure("cache volume should be namespaced").
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

func (s *cacheSchema) cacheVolume(ctx context.Context, parent dagql.Instance[*core.Query], args cacheArgs) (dagql.Instance[*core.CacheVolume], error) {
	var inst dagql.Instance[*core.CacheVolume]

	if args.Namespace != "" {
		return dagql.NewInstanceForCurrentID(ctx, s.srv, parent, core.NewCache(args.Namespace+":"+args.Key))
	}

	m, err := parent.Self.Server.CurrentModule(ctx)
	if err != nil && !errors.Is(err, core.ErrNoCurrentModule) {
		return inst, err
	}

	namespaceKey := ""
	if m != nil {
		name, err := m.Source.Self.ModuleName(ctx)
		if err != nil {
			return inst, err
		}

		symbolic, err := m.Source.Self.Symbolic()
		if err != nil {
			return inst, err
		}

		namespaceKey = "mod(" + name + symbolic + ")"
	}

	// if no namespace key, just return the NewCache based on key
	if namespaceKey == "" {
		return dagql.NewInstanceForCurrentID(ctx, s.srv, parent, core.NewCache(":"+args.Key))
	}

	err = s.srv.Select(ctx, s.srv.Root(), &inst, dagql.Selector{
		Field: "cacheVolume",
		Pure:  true,
		Args: []dagql.NamedInput{
			{
				Name:  "namespace",
				Value: dagql.NewString(namespaceKey),
			},
			{
				Name:  "key",
				Value: dagql.NewString(args.Key),
			},
		},
	})
	if err != nil {
		return inst, err
	}

	return inst, nil
}
