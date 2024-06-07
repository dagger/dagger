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
	slog.Error("ModuleEnumType.ConvertFromSDKResult: %v", value)

	if value == nil {
		slog.Warn("ModuleEnumType.ConvertFromSDKResult: got nil value")
		return nil, nil
	}

	switch value := value.(type) {
	case string:
		typeDef := dagql.NewDynamicEnum(m.typeDef)

		for _, v := range m.typeDef.Values {
			typeDef = typeDef.Register(v.OriginalName, v.Description)
		}

		val, err := typeDef.DecodeInput(value)
		if err != nil {
			return nil, fmt.Errorf("ModuleEnumType.ConvertFromSDKResult: invalid enum value %q for %q", value, m.typeDef.Name)
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
	slog.Error("ModuleEnumType.ConvertToSDKInput: %v", value)

	if value == nil {
		return nil, nil
	}

	switch x := value.(type) {
	case dagql.Scalar[dagql.String]:
		typeDef := dagql.NewDynamicEnum(m.typeDef)

		for _, v := range m.typeDef.Values {
			typeDef = typeDef.Register(v.OriginalName, v.Description)
		}

		val, err := typeDef.DecodeInput(value)
		if err != nil {
			return nil, fmt.Errorf("ModuleEnumType.ConvertToSDKInput: invalid enum value %q for %q", x.Value, m.typeDef.Name)
		}

		return val, nil
	default:
		return nil, fmt.Errorf("%T.ConvertToSDKInput cannot handle type %T", m, x)
	}
}
