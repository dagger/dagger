package schema

import (
	"context"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
)

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

func collectInputs[T dagql.Type](inputs dagql.Optional[dagql.ArrayInput[dagql.InputObject[T]]]) []T {
	if !inputs.Valid {
		return nil
	}
	ts := make([]T, len(inputs.Value))
	for i, input := range inputs.Value {
		ts[i] = input.Value
	}
	return ts
}

func collectInputsSlice[T dagql.Type](inputs []dagql.InputObject[T]) []T {
	ts := make([]T, len(inputs))
	for i, input := range inputs {
		ts[i] = input.Value
	}
	return ts
}

func collectArrayInput[T any, I dagql.Input](in dagql.ArrayInput[I], conv func(I) T) []T {
	ts := make([]T, len(in))
	for i, v := range in {
		ts[i] = conv(v)
	}
	return ts
}

func collectIDObjects[T dagql.Typed](ctx context.Context, srv *dagql.Server, ids []dagql.ID[T]) ([]T, error) {
	ts := make([]T, len(ids))
	for i, id := range ids {
		inst, err := id.Load(ctx, srv)
		if err != nil {
			return nil, err
		}
		ts[i] = inst.Self
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
