package schema

import (
	"context"

	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/dagql"
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
