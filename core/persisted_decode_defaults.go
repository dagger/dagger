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
func persistedDecodeDefaultDeps(ctx context.Context, dec *dagql.PersistDecodeContext) (*SchemaBuilder, error) {
	query, err := persistedDecodeQuery(dec)
	if err != nil {
		return nil, err
	}
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
