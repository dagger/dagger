package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

// PersistedDecodeDefaultsFactory builds the default module dependencies a
// persisted Module decode needs, from already-initialized core descriptors
// alone. It creates no client, evaluates no schema, begins no cache
// operation, evaluates no lazy operation and starts no service.
type PersistedDecodeDefaultsFactory func(view call.View) *SchemaBuilder

type persistedDecodeDefaults struct {
	root    *Query
	factory PersistedDecodeDefaultsFactory
}

type persistedDecodeDefaultsKey struct{}

// ContextWithPersistedDecodeDefaults binds the engine root and the pure
// default-dependency factory for a background persisted decode. Only the
// engine's registered sharing preparation callback calls it.
func ContextWithPersistedDecodeDefaults(ctx context.Context, root *Query, factory PersistedDecodeDefaultsFactory) context.Context {
	if root == nil || factory == nil {
		return ctx
	}
	return context.WithValue(ctx, persistedDecodeDefaultsKey{}, &persistedDecodeDefaults{root: root, factory: factory})
}

// persistedDecodeDefaultDeps stands at the persisted Module decoder's
// DefaultDeps call. Outside a marked preparation context it keeps the
// ordinary Query.DefaultDeps behavior, so Server.DefaultDeps and general
// client authority are unchanged. Inside one it requires the registered root
// and factory, checks that the decoding server really has that engine root,
// and returns a fresh builder for that server's view: the same immutable
// core view the decoding fork uses. Nested Module decodes make their own.
func persistedDecodeDefaultDeps(ctx context.Context, dec *dagql.PersistDecodeContext, query *Query) (*SchemaBuilder, error) {
	if !engine.IsSnapshotSharePreparation(ctx) {
		return query.DefaultDeps(ctx)
	}
	defaults, _ := ctx.Value(persistedDecodeDefaultsKey{}).(*persistedDecodeDefaults)
	if defaults == nil {
		return nil, fmt.Errorf("%w: persisted module decode has no registered default dependencies", engine.ErrSnapshotShareEvaluation)
	}
	if query != defaults.root {
		return nil, fmt.Errorf("%w: persisted module decode server does not carry the registered engine root", engine.ErrSnapshotShareEvaluation)
	}
	deps := defaults.factory(dec.Server().View)
	if deps == nil {
		return nil, fmt.Errorf("%w: registered default dependency factory returned nothing", engine.ErrSnapshotShareEvaluation)
	}
	return deps, nil
}

// CheckPersistedDecodeDefaults verifies, before any shared decode attempt is
// started with ctx and dag, what persistedDecodeDefaultDeps would otherwise
// discover inside one: ctx carries registered defaults, dag's root is that
// registered engine root, and the factory yields a builder for dag's view.
// The engine's preparation callback calls it on every preparation context it
// hands out.
func CheckPersistedDecodeDefaults(ctx context.Context, dag *dagql.Server) error {
	defaults, _ := ctx.Value(persistedDecodeDefaultsKey{}).(*persistedDecodeDefaults)
	if defaults == nil {
		return fmt.Errorf("persisted decode defaults: none registered")
	}
	if dag == nil || dag.Root() == nil {
		return fmt.Errorf("persisted decode defaults: no decoding server root")
	}
	query, ok := dagql.UnwrapAs[*Query](dag.Root())
	if !ok || query != defaults.root {
		return fmt.Errorf("persisted decode defaults: the decoding server does not carry the registered engine root")
	}
	if defaults.factory(dag.View) == nil {
		return fmt.Errorf("persisted decode defaults: the registered factory returned nothing")
	}
	return nil
}
