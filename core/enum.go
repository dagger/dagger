package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
	"github.com/vektah/gqlparser/v2/ast"
)

type ModuleEnumType struct {
	typeDef *EnumTypeDef
	mod *Module
}

func (m *ModuleEnumType) SourceMod() Mod {
	if m.mod == nil {
		return nil
	}

	return m.mod
}

func (m *ModuleEnumType) ConvertFromSDKResult(ctx context.Context, value any) (dagql.Typed, error) {
	if value == nil {
		slog.Warn("ModuleEnumType.ConvertFromSDKResult: got nil value")
		return nil, nil
	}

	switch value := value.(type) {
	case string:
		return dagql.NewDynamicEnumValue(&EnumObject{Module: m.mod, TypeDef: m.typeDef}, value), nil
	default:
		return nil, fmt.Errorf("unexpected result value type %T for enum %q", value, m.typeDef.Name)
	}
}

func (m *ModuleEnumType) TypeDef() *TypeDef {
	return &TypeDef{
		Kind: TypeDefKindEnum,
		AsEnum: dagql.NonNull(m.typeDef),
	}
}

func (m *ModuleEnumType) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	if value == nil {
		return nil, nil
	}

	// TODO: refactor to not use DynamicID since Enum are not
	switch x := value.(type) {
	case DynamicID:
		deps, err := m.mod.Query.IDDeps(ctx, x.ID())
		if err != nil {
			return nil, fmt.Errorf("ModuleEnumType.ConvertToSDKInput: failed to get deps for DynamicID: %w", err)
		}

		dag, err := deps.Schema(ctx)
		if err != nil {
			return nil, fmt.Errorf("ModuleEnumType.ConvertToSDKInput: failed to get schema for DynamicID: %w", err)
		}

		val, err := dag.Load(ctx, x.ID())
		if err != nil {
			return nil, fmt.Errorf("ModuleEnumType.ConvertToSDKInput: failed to load DynamicID: %w", err)
		}

		switch x := val.(type) {
		case dagql.DynamicEnumValue:
			return x.DecodeInput(x)
		default:
			return nil, fmt.Errorf("ModuleEnumType.ConvertToSDKInput: unexpected value type %T", x)
		}
	default:
		return nil, fmt.Errorf("%T.ConvertToSDKInput cannot handle type %T", m, x)
	}
}

type EnumObject struct {
	Module  *Module
	TypeDef *EnumTypeDef
}

func (enum *EnumObject) TypeName() string {
	return enum.TypeDef.Name
}

func (enum *EnumObject) Type() *ast.Type {
	return &ast.Type{
		NamedType: enum.TypeDef.Name,
		NonNull:   true,
	}
}

func (enum *EnumObject) PossibleValues() ast.EnumValueList {
	var values ast.EnumValueList

	for _, value := range enum.TypeDef.Values {
		values = append(values, &ast.EnumValueDefinition{
			Name:        value.Name,
			Description: formatGqlDescription(value.Description),
		})
	}

	return values
}

func (enum *EnumObject) TypeDefinition() *ast.Definition {
	return &ast.Definition{
		Kind: ast.Enum,
		Name: enum.TypeName(),
		EnumValues: enum.PossibleValues(),
		Description: enum.TypeDescription(),
	}
}

func (enum *EnumObject) DecodeInput(val any) (dagql.Input, error) {
	switch val := val.(type) {
	case string:
		return dagql.NewDynamicEnumValue(enum, val), nil	
	default:
		return nil, fmt.Errorf("invalid enum value: %v", val)
	}
}

func (enum *EnumObject) TypeDescription() string {
	return formatGqlDescription(enum.TypeDef.Description)
}

func (enum *EnumObject) Install(ctx context.Context, dag *dagql.Server) error {
	if enum.Module.InstanceID == nil {
		return fmt.Errorf("installing object %q too early", enum.TypeName())
	}

	dag.InstallTypeDef(enum)

	return nil
}
