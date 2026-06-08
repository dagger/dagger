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

// withFinalTypeName overrides the name of an object/interface/enum typedef,
// storing it verbatim instead of re-normalizing it.
//
// Callers that *reconstruct* a typedef referencing a type by its already-final,
// module-namespaced GraphQL name (e.g. "ModuleA_Overlay") — whether to serve it
// or to look it up via Deps.ModTypeFor — build it with the public
// withObject/withInterface/withEnum fields, which run the name through
// strcase.ToCamel. That is correct when an SDK supplies a raw type name, but
// wrong for an already-final name: ToCamel is not idempotent and collapses the
// "_" namespace separator, so "ModuleA_Overlay" becomes "ModuleAOverlay",
// diverging from the installed type and breaking cross-module references and
// matching. This re-applies the intended name with no normalization (the
// __withName fields store verbatim; see (*ObjectTypeDef).WithName).
func withFinalTypeName(ctx context.Context, td dagql.ObjectResult[*TypeDef], name string) (dagql.ObjectResult[*TypeDef], error) {
	if td.Self() == nil || td.Self().Name == name {
		return td, nil
	}
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return td, err
	}
	rename := dagql.Selector{
		Field: "__withName",
		Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(name)}},
	}
	switch td.Self().Kind {
	case TypeDefKindObject:
		var renamed dagql.ObjectResult[*ObjectTypeDef]
		if err := dag.Select(ctx, td.Self().AsObject.Value, &renamed, rename); err != nil {
			return td, err
		}
		id, err := ResultIDInput(renamed)
		if err != nil {
			return td, err
		}
		var out dagql.ObjectResult[*TypeDef]
		if err := dag.Select(ctx, td, &out, dagql.Selector{
			Field: "__withObjectTypeDef",
			Args:  []dagql.NamedInput{{Name: "objectTypeDef", Value: id}},
		}); err != nil {
			return td, err
		}
		return out, nil
	case TypeDefKindInterface:
		var renamed dagql.ObjectResult[*InterfaceTypeDef]
		if err := dag.Select(ctx, td.Self().AsInterface.Value, &renamed, rename); err != nil {
			return td, err
		}
		id, err := ResultIDInput(renamed)
		if err != nil {
			return td, err
		}
		var out dagql.ObjectResult[*TypeDef]
		if err := dag.Select(ctx, td, &out, dagql.Selector{
			Field: "__withInterfaceTypeDef",
			Args:  []dagql.NamedInput{{Name: "interfaceTypeDef", Value: id}},
		}); err != nil {
			return td, err
		}
		return out, nil
	case TypeDefKindEnum:
		var renamed dagql.ObjectResult[*EnumTypeDef]
		if err := dag.Select(ctx, td.Self().AsEnum.Value, &renamed, rename); err != nil {
			return td, err
		}
		id, err := ResultIDInput(renamed)
		if err != nil {
			return td, err
		}
		var out dagql.ObjectResult[*TypeDef]
		if err := dag.Select(ctx, td, &out, dagql.Selector{
			Field: "__withEnumTypeDef",
			Args:  []dagql.NamedInput{{Name: "enumTypeDef", Value: id}},
		}); err != nil {
			return td, err
		}
		return out, nil
	default:
		return td, nil
	}
}

// SelectReferenceTypeDef builds a typedef that references an existing type by
// its already-final GraphQL name, preserving that name verbatim. Use this
// instead of a bare withObject/withInterface/withEnum selector whenever the name
// is a final (possibly module-namespaced) type name rather than a raw SDK name.
// See withFinalTypeName for why re-normalization must be avoided.
func SelectReferenceTypeDef(ctx context.Context, field, argName, name string, extraArgs ...dagql.NamedInput) (dagql.ObjectResult[*TypeDef], error) {
	args := append([]dagql.NamedInput{{Name: argName, Value: dagql.String(name)}}, extraArgs...)
	td, err := SelectTypeDef(ctx, dagql.Selector{Field: field, Args: args})
	if err != nil {
		return td, err
	}
	return withFinalTypeName(ctx, td, name)
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
