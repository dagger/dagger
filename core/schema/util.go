package schema

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/introspection"
)

type SchemaResolvers interface {
	Install(*dagql.Server)
}

func Syncer[T dagql.Typed]() dagql.Field[T] {
	return dagql.NodeFunc("sync", func(ctx context.Context, self dagql.ObjectResult[T], args struct {
		Recipe bool `default:"false" internal:"true"`
	}) (res dagql.Result[dagql.ID[T]], _ error) {
		if _, ok := dagql.UnwrapAs[dagql.HasLazyEvaluation](self); ok {
			cache, err := dagql.EngineCache(ctx)
			if err != nil {
				return res, err
			}
			if err := cache.Evaluate(ctx, self); err != nil {
				return res, err
			}
		} else {
			syncable, ok := dagql.UnwrapAs[core.Syncable](self)
			if !ok {
				return res, fmt.Errorf("internal error: %T does not support sync", self.Self())
			}
			if err := syncable.Sync(ctx); err != nil {
				return res, err
			}
		}
		var selfID *call.ID
		var err error
		if args.Recipe {
			selfID, err = self.RecipeID(ctx)
		} else {
			selfID, err = self.ID()
		}
		if err != nil {
			return res, err
		}
		id := dagql.NewID[T](selfID)
		return dagql.NewResultForCurrentCall(ctx, id)
	}).DoNotCache("sync is an operational boundary and each object already controls its own underlying lazy/cached state")
}

func collectInputsSlice[T dagql.Type](inputs []dagql.InputObject[T]) []T {
	ts := make([]T, len(inputs))
	for i, input := range inputs {
		ts[i] = input.Value
	}
	return ts
}

func collectIDObjectResults[T dagql.Typed](ctx context.Context, srv *dagql.Server, ids []dagql.ID[T]) ([]dagql.ObjectResult[T], error) {
	ts := make([]dagql.ObjectResult[T], len(ids))
	for i, id := range ids {
		inst, err := id.Load(ctx, srv)
		if err != nil {
			return nil, err
		}
		ts[i] = inst
	}
	return ts, nil
}

func asArrayInput[T any, I dagql.Input](ts []T, conv func(T) I) dagql.ArrayInput[I] {
	ins := make(dagql.ArrayInput[I], len(ts))
	for i, v := range ts {
		ins[i] = conv(v)
	}
	return ins
}

func SchemaIntrospectionJSON(ctx context.Context, dag *dagql.Server) (json.RawMessage, error) {
	data, err := dag.Query(ctx, introspection.Query, nil)
	if err != nil {
		return nil, fmt.Errorf("introspection query failed: %w", err)
	}
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal introspection result: %w", err)
	}
	return json.RawMessage(jsonBytes), nil
}

func ptr[T any](v T) *T {
	return &v
}

var AllVersion = core.AllVersion

type BeforeVersion = core.BeforeVersion
type AfterVersion = core.AfterVersion
