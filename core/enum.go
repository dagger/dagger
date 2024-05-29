package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

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

	dag.InstallScalar(enum)

	return nil
}
