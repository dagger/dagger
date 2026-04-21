package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

func (s *moduleSchema) functionArgs(
	ctx context.Context,
	fn *core.Function,
	_ struct{},
) (dagql.ObjectResultArray[*core.FunctionArg], error) {
	return fn.Args, nil
}

func (s *moduleSchema) functionReturnType(
	ctx context.Context,
	fn *core.Function,
	_ struct{},
) (dagql.ObjectResult[*core.TypeDef], error) {
	return fn.ReturnType, nil
}

func (s *moduleSchema) typeDefAsList(
	ctx context.Context,
	typeDef *core.TypeDef,
	_ struct{},
) (dagql.Nullable[dagql.ObjectResult[*core.ListTypeDef]], error) {
	return typeDef.AsList, nil
}

func (s *moduleSchema) typeDefAsObject(
	ctx context.Context,
	typeDef *core.TypeDef,
	_ struct{},
) (dagql.Nullable[dagql.ObjectResult[*core.ObjectTypeDef]], error) {
	return typeDef.AsObject, nil
}

func (s *moduleSchema) typeDefAsInterface(
	ctx context.Context,
	typeDef *core.TypeDef,
	_ struct{},
) (dagql.Nullable[dagql.ObjectResult[*core.InterfaceTypeDef]], error) {
	return typeDef.AsInterface, nil
}

func (s *moduleSchema) typeDefAsInput(
	ctx context.Context,
	typeDef *core.TypeDef,
	_ struct{},
) (dagql.Nullable[dagql.ObjectResult[*core.InputTypeDef]], error) {
	return typeDef.AsInput, nil
}

func (s *moduleSchema) typeDefAsScalar(
	ctx context.Context,
	typeDef *core.TypeDef,
	_ struct{},
) (dagql.Nullable[dagql.ObjectResult[*core.ScalarTypeDef]], error) {
	return typeDef.AsScalar, nil
}

func (s *moduleSchema) typeDefAsEnum(
	ctx context.Context,
	typeDef *core.TypeDef,
	_ struct{},
) (dagql.Nullable[dagql.ObjectResult[*core.EnumTypeDef]], error) {
	return typeDef.AsEnum, nil
}

func (s *moduleSchema) functionArgTypeDef(
	ctx context.Context,
	arg *core.FunctionArg,
	_ struct{},
) (dagql.ObjectResult[*core.TypeDef], error) {
	return arg.TypeDef, nil
}

func (s *moduleSchema) fieldTypeDefTypeDef(
	ctx context.Context,
	field *core.FieldTypeDef,
	_ struct{},
) (dagql.ObjectResult[*core.TypeDef], error) {
	return field.TypeDef, nil
}

func (s *moduleSchema) objectTypeDefFields(
	ctx context.Context,
	obj *core.ObjectTypeDef,
	_ struct{},
) (dagql.ObjectResultArray[*core.FieldTypeDef], error) {
	return obj.Fields, nil
}

func (s *moduleSchema) objectTypeDefFunctions(
	ctx context.Context,
	obj *core.ObjectTypeDef,
	_ struct{},
) (dagql.ObjectResultArray[*core.Function], error) {
	return obj.Functions, nil
}

func (s *moduleSchema) objectTypeDefConstructor(
	ctx context.Context,
	obj *core.ObjectTypeDef,
	_ struct{},
) (dagql.Nullable[dagql.ObjectResult[*core.Function]], error) {
	return obj.Constructor, nil
}

func (s *moduleSchema) interfaceTypeDefFunctions(
	ctx context.Context,
	iface *core.InterfaceTypeDef,
	_ struct{},
) (dagql.ObjectResultArray[*core.Function], error) {
	return iface.Functions, nil
}

func (s *moduleSchema) listElementTypeDef(
	ctx context.Context,
	list *core.ListTypeDef,
	_ struct{},
) (dagql.ObjectResult[*core.TypeDef], error) {
	return list.ElementTypeDef, nil
}

func (s *moduleSchema) inputTypeDefFields(
	ctx context.Context,
	input *core.InputTypeDef,
	_ struct{},
) (dagql.ObjectResultArray[*core.FieldTypeDef], error) {
	return input.Fields, nil
}

func (s *moduleSchema) enumTypeDefMembers(
	ctx context.Context,
	enum *core.EnumTypeDef,
	_ struct{},
) (dagql.ObjectResultArray[*core.EnumMemberTypeDef], error) {
	return enum.Members, nil
}

func (s *moduleSchema) enumTypeDefValues(
	ctx context.Context,
	enum *core.EnumTypeDef,
	_ struct{},
) (dagql.ObjectResultArray[*core.EnumMemberTypeDef], error) {
	return s.enumTypeDefMembers(ctx, enum, struct{}{})
}
