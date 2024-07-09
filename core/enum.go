package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
	"github.com/vektah/gqlparser/v2/ast"
)

type ModuleEnumType struct {
	typeDef *EnumTypeDef
	mod     *Module
}

func (m *ModuleEnumType) SourceMod() Mod {
	if m.mod == nil {
		return nil
	}
	return m.mod
}

func (m *ModuleEnumType) TypeDef() *TypeDef {
	return &TypeDef{
		Kind:   TypeDefKindEnum,
		AsEnum: dagql.NonNull(m.typeDef),
	}
}

func (m *ModuleEnumType) ConvertFromSDKResult(ctx context.Context, value any) (dagql.Typed, error) {
	if value == nil {
		slog.Warn("%T.ConvertFromSDKResult: got nil value", m)
		return nil, nil
	}

	switch value := value.(type) {
	case string:
		decoder, err := m.getDecoder(ctx)
		if err != nil {
			return nil, fmt.Errorf("%T.ConvertFromSDKResult: failed to get decoder: %w", m, err)
		}

		val, err := decoder.DecodeInput(value)
		if err != nil {
			return nil, fmt.Errorf("%T.ConvertFromSDKResult: invalid enum value %q for %q", m, value, m.typeDef.Name)
		}

		return val, nil
	default:
		return nil, fmt.Errorf("unexpected result value type %T for enum %q", value, m.typeDef.Name)
	}
}

func (m *ModuleEnumType) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	if value == nil {
		return nil, nil
	}

	var val string
	switch x := value.(type) {
	case *ModuleEnum:
		val = x.Value
	case dagql.Scalar[dagql.String]:
		val = string(x.Value)
	default:
		return nil, fmt.Errorf("%T.ConvertToSDKInput cannot handle type %T", m, x)
	}

	decoder, err := m.getDecoder(ctx)
	if err != nil {
		return nil, fmt.Errorf("%T.ConvertToSDKInput: failed to get decoder: %w", m, err)
	}
	return decoder.DecodeInput(val)
}

func (m *ModuleEnumType) getDecoder(ctx context.Context) (dagql.InputDecoder, error) {
	// Check the dependencies
	srv, err := m.mod.Deps.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("%T.getDecoder: failed to get schema: %w", m, err)
	}

	enumType, ok := srv.ScalarType(m.typeDef.Name)
	if ok {
		return enumType, nil
	}

	// If not check if the enum is part of its own module
	for _, enumTypeDef := range m.mod.EnumDefs {
		if enumTypeDef.AsEnum.Value.Name == m.typeDef.Name {
			return &ModuleEnum{TypeDef: enumTypeDef.AsEnum.Value}, nil
		}
	}

	return nil, fmt.Errorf("%T.getDecoder: failed to get enum type %q", m, m.typeDef.Name)
}

type ModuleEnum struct {
	TypeDef       *EnumTypeDef
	Value         string
	OriginalValue string
}

func (e *ModuleEnum) TypeName() string {
	return e.TypeDef.Name
}

func (e *ModuleEnum) Type() *ast.Type {
	return &ast.Type{
		NamedType: e.TypeDef.Name,
		NonNull:   true,
	}
}

func (e *ModuleEnum) TypeDescription() string {
	return formatGqlDescription(e.TypeDef.Description)
}

func (e *ModuleEnum) TypeDefinition(views ...string) *ast.Definition {
	return &ast.Definition{
		Kind:        ast.Enum,
		Name:        e.TypeName(),
		EnumValues:  e.PossibleValues(),
		Description: e.TypeDescription(),
	}
}

func (e *ModuleEnum) PossibleValues() ast.EnumValueList {
	var values ast.EnumValueList
	for _, val := range e.TypeDef.Values {
		values = append(values, &ast.EnumValueDefinition{
			Name:        val.Name,
			Description: val.Description,
		})
	}

	return values
}

func (e *ModuleEnum) Install(dag *dagql.Server) error {
	dag.InstallScalar(e)
	return nil
}

func (e *ModuleEnum) ToLiteral() call.Literal {
	return call.NewLiteralEnum(e.Value)
}

func (e *ModuleEnum) Decoder() dagql.InputDecoder {
	return e
}

func (e *ModuleEnum) DecodeInput(val any) (dagql.Input, error) {
	switch x := val.(type) {
	case string:
		return e.Lookup(x)
	case dagql.Scalar[dagql.String]:
		return e.Lookup(string(x.Value))
	default:
		return nil, fmt.Errorf("cannot create dynamic Enum from %T", x)
	}
}

func (e *ModuleEnum) Lookup(val string) (dagql.Input, error) {
	for _, possible := range e.TypeDef.Values {
		if val == possible.Name || val == possible.OriginalName {
			return &ModuleEnum{
				TypeDef:       e.TypeDef,
				Value:         possible.Name,
				OriginalValue: possible.OriginalName,
			}, nil
		}
	}

	return nil, fmt.Errorf("invalid enum value %q", val)
}

func (e *ModuleEnum) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.OriginalValue)
}
