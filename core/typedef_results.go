package core

import (
	"context"
	"reflect"

	"github.com/dagger/dagger/dagql"
)

func SelectTypeDef(ctx context.Context, sels ...dagql.Selector) (dagql.ObjectResult[*TypeDef], error) {
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*TypeDef]{}, err
	}
	return SelectTypeDefWithServer(ctx, dag, sels...)
}

func SelectTypeDefWithServer(ctx context.Context, dag *dagql.Server, sels ...dagql.Selector) (dagql.ObjectResult[*TypeDef], error) {
	var inst dagql.ObjectResult[*TypeDef]
	query := append([]dagql.Selector{{Field: "typeDef"}}, sels...)
	if err := dag.Select(ctx, dag.Root(), &inst, query...); err != nil {
		return inst, err
	}
	return inst, nil
}

func SelectFunctionWithServer(ctx context.Context, dag *dagql.Server, name string, returnType dagql.ObjectResult[*TypeDef]) (dagql.ObjectResult[*Function], error) {
	returnTypeID, err := ResultIDInput(returnType)
	if err != nil {
		return dagql.ObjectResult[*Function]{}, err
	}
	var inst dagql.ObjectResult[*Function]
	if err := dag.Select(ctx, dag.Root(), &inst, dagql.Selector{
		Field: "function",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String(name)},
			{Name: "returnType", Value: returnTypeID},
		},
	}); err != nil {
		return inst, err
	}
	return inst, nil
}

func ResultIDInput[T dagql.Typed](res dagql.ObjectResult[T]) (dagql.ID[T], error) {
	id, err := res.ID()
	if err != nil {
		return dagql.ID[T]{}, err
	}
	return dagql.NewID[T](id), nil
}

func OptionalResultIDInput[T dagql.Typed](res dagql.ObjectResult[T]) (dagql.Optional[dagql.ID[T]], error) {
	self := reflect.ValueOf(res.Self())
	if !self.IsValid() || (self.Kind() == reflect.Pointer && self.IsNil()) {
		return dagql.Optional[dagql.ID[T]]{}, nil
	}
	id, err := res.ID()
	if err != nil {
		return dagql.Optional[dagql.ID[T]]{}, err
	}
	if id == nil {
		return dagql.Optional[dagql.ID[T]]{}, nil
	}
	return dagql.Opt(dagql.NewID[T](id)), nil
}

func OptSourceModuleName(name string) dagql.Optional[dagql.String] {
	if name == "" {
		return dagql.Optional[dagql.String]{}
	}
	return dagql.Opt(dagql.String(name))
}

func OptString(v *string) dagql.Optional[dagql.String] {
	if v == nil {
		return dagql.Optional[dagql.String]{}
	}
	return dagql.Opt(dagql.String(*v))
}
