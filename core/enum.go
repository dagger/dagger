package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
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

func (m *ModuleEnumType) ConvertFromSDKResult(ctx context.Context, value any) (dagql.Typed, error) {
	if value == nil {
		slog.Warn("ModuleEnumType.ConvertFromSDKResult: got nil value")
		return nil, nil
	}

	switch value := value.(type) {
	case string:
		decoder, err := m.getDecoder(ctx)
		if err != nil {
			return nil, fmt.Errorf("ModuleEnumType.ConvertToSDKInput: failed to get decoder: %w", err)
		}

		val, err := decoder.DecodeInput(value)
		if err != nil {
			return nil, fmt.Errorf("ModuleEnumType.ConvertToSDKInput: invalid enum value %q for %q", value, m.typeDef.Name)
		}

		return val, nil
	default:
		return nil, fmt.Errorf("unexpected result value type %T for enum %q", value, m.typeDef.Name)
	}
}

func (m *ModuleEnumType) TypeDef() *TypeDef {
	return &TypeDef{
		Kind:   TypeDefKindEnum,
		AsEnum: dagql.NonNull(m.typeDef),
	}
}

func (m *ModuleEnumType) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	if value == nil {
		return nil, nil
	}

	switch x := value.(type) {
	case *dagql.DynamicEnumValue:
		decoder, err := m.getDecoder(ctx)
		if err != nil {
			return nil, fmt.Errorf("ModuleEnumType.ConvertToSDKInput: failed to get decoder: %w", err)
		}

		val, err := decoder.DecodeInput(x.Value())
		if err != nil {
			return nil, fmt.Errorf("ModuleEnumType.ConvertToSDKInput: invalid enum value %q for %q", x.Value(), m.typeDef.Name)
		}

		return val, nil
	default:
		return nil, fmt.Errorf("%T.ConvertToSDKInput cannot handle type %T", m, x)
	}
}

func (m *ModuleEnumType) getDecoder(ctx context.Context) (dagql.InputDecoder, error) {
	// Check the dependencies
	srv, err := m.mod.Deps.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("ModuleEnumType.getDecoder: failed to get schema: %w", err)
	}

	enumType, ok := srv.ScalarType(m.typeDef.Name)
	if ok {
		return enumType, nil
	}

	// If not check if the enum is part of its own module
	for _, enumTypeDef := range m.mod.EnumDefs {
		if enumTypeDef.AsEnum.Value.Name == m.typeDef.Name {
			return dagql.NewDynamicEnum(enumTypeDef.AsEnum.Value), nil
		}
	}

	return nil, fmt.Errorf("ModuleEnumType.getDecoder: failed to get enum type %q", m.typeDef.Name)
}
