package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
)

type Evaluatable interface {
	dagql.Typed
	Evaluate(context.Context) error
}

func Syncer[T Evaluatable]() dagql.Field[T] {
	return dagql.NodeFunc("sync", func(ctx context.Context, self dagql.Instance[T], _ struct{}) (dagql.ID[T], error) {
		err := self.Self.Evaluate(ctx)
		if err != nil {
			var zero dagql.ID[T]
			return zero, err
		}
		return dagql.NewID[T](self.ID()), nil
	})
}

func DynamicSyncer[T Evaluatable](t dagql.Typed) dagql.Field[T] {
	field := dagql.NodeFunc("sync", func(ctx context.Context, self dagql.Instance[T], _ struct{}) (dagql.ID[T], error) {
		err := self.Self.Evaluate(ctx)
		if err != nil {
			var zero dagql.ID[T]
			return zero, err
		}
		return dagql.NewDynamicID[T](self.ID(), self.Self), nil
	})
	field.Spec.Type = dagql.NewDynamicID(nil, t)
	return field
}
