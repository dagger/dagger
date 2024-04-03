package schema

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/introspection"
	"github.com/dagger/dagger/engine/buildkit"
)

type SchemaResolvers interface {
	Install()
}

type Evaluatable interface {
	dagql.Typed
	Evaluate(context.Context) (*buildkit.Result, error)
}

func Syncer[T Evaluatable]() dagql.Field[T] {
	return dagql.NodeFunc("sync", func(ctx context.Context, self dagql.Instance[T], _ struct{}) (dagql.ID[T], error) {
		_, err := self.Self.Evaluate(ctx)
		if err != nil {
			var zero dagql.ID[T]
			return zero, err
		}
		return dagql.NewID[T](self.ID()), nil
	})
}

func collectInputsSlice[T dagql.Type](inputs []dagql.InputObject[T]) []T {
	ts := make([]T, len(inputs))
	for i, input := range inputs {
		ts[i] = input.Value
	}
	return ts
}

func collectIDInstances[T dagql.Typed](ctx context.Context, srv *dagql.Server, ids []dagql.ID[T]) ([]dagql.Instance[T], error) {
	ts := make([]dagql.Instance[T], len(ids))
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

func schemaIntrospectionJSON(ctx context.Context, dag *dagql.Server) (json.RawMessage, error) {
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

func gqlFieldName(name string) string {
	// gql field name is uncapitalized camel case
	return strcase.ToLowerCamel(name)
}
