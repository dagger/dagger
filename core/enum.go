package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
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
		enum, local, err := m.getEnum(ctx)
		if err != nil {
			return nil, fmt.Errorf("%T.ConvertFromSDKResult: failed to get enum: %w", m, err)
		}
		// XXX:
		// targetName := ...
		for _, member := range enum.TypeDef.Members {
			if local && member.OriginalName == value {
				return enum.DecodeInput(member.Name)
			} else if !local && member.Name == value {
				return enum.DecodeInput(member.Name)
			}
		}
		for _, member := range enum.TypeDef.Members {
			if member.Value == value {
				return enum.DecodeInput(member.Name)
			}
		}
		return nil, fmt.Errorf("%T.ConvertFromSDKResult: invalid enum value %q for %q", m, value, m.typeDef.Name)
	default:
		return nil, fmt.Errorf("unexpected result value type %T for enum %q", value, m.typeDef.Name)
	}
}

func (m *ModuleEnumType) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	if value == nil {
		return nil, nil
	}

	enum, local, err := m.getEnum(ctx)
	if err != nil {
		return nil, fmt.Errorf("%T.ConvertFromSDKResult: failed to get enum: %w", m, err)
	}
	result, err := enum.DecodeInput(value)
	if err != nil {
		return nil, err
	}
	enum = result.(*ModuleEnum)
	if local {
		return enum.getTypeDef().OriginalName, nil
	}
	return enum.getTypeDef().Name, nil
}

func (m *ModuleEnumType) CollectCoreIDs(ctx context.Context, value dagql.Typed, ids map[digest.Digest]*resource.ID) error {
	return nil
}

func (m *ModuleEnumType) getEnum(ctx context.Context) (*ModuleEnum, bool, error) {
	// Check the dependencies
	srv, err := m.mod.Deps.Schema(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("%T.getDecoder: failed to get schema: %w", m, err)
	}

	scalar, ok := srv.ScalarType(m.typeDef.Name)
	if ok {
		enum, ok := scalar.(*ModuleEnum)
		if !ok {
			return nil, false, fmt.Errorf("%T.getDecoder: incorrect type %T for scalar", m, scalar)
		}
		return enum, false, nil
	}

	// If not check if the enum is part of its own module
	for _, enumTypeDef := range m.mod.EnumDefs {
		if enumTypeDef.AsEnum.Value.Name == m.typeDef.Name {
			return &ModuleEnum{TypeDef: enumTypeDef.AsEnum.Value}, true, nil
		}
	}

	return nil, false, fmt.Errorf("%T.getDecoder: failed to get enum type %q", m, m.typeDef.Name)
}

type ModuleEnum struct {
	TypeDef *EnumTypeDef
	Name    string
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

func (e *ModuleEnum) TypeDefinition(view string) *ast.Definition {
	def := &ast.Definition{
		Kind:        ast.Enum,
		Name:        e.TypeName(),
		EnumValues:  e.PossibleValues(),
		Description: e.TypeDescription(),
	}
	if e.TypeDef.SourceMap != nil {
		def.Directives = append(def.Directives, e.TypeDef.SourceMap.TypeDirective())
	}
	return def
}

func (e *ModuleEnum) PossibleValues() ast.EnumValueList {
	var values ast.EnumValueList
	for _, val := range e.TypeDef.Members {
		def := &ast.EnumValueDefinition{
			Name:        val.Name,
			Description: val.Description,
			Directives:  []*ast.Directive{val.EnumValueDirective()},
		}
		if val.SourceMap != nil {
			def.Directives = append(def.Directives, val.SourceMap.TypeDirective())
		}
		values = append(values, def)
	}

	return values
}

func (e *ModuleEnum) Install(dag *dagql.Server) error {
	dag.InstallScalar(e)
	return nil
}

func (e *ModuleEnum) ToLiteral() call.Literal {
	return call.NewLiteralEnum(e.Name)
}

func (e *ModuleEnum) Decoder() dagql.InputDecoder {
	return e
}

func (e *ModuleEnum) DecodeInput(val any) (dagql.Input, error) {
	if enum, ok := val.(*ModuleEnum); ok {
		val = enum.Name
	}

	v, err := (&dagql.EnumValueName{Enum: e.TypeName()}).DecodeInput(val)
	if err != nil {
		return nil, err
	}
	return e.Lookup(v.(*dagql.EnumValueName).Name)
}

func (e *ModuleEnum) Lookup(val string) (dagql.Input, error) {
	for _, possible := range e.TypeDef.Members {
		if val == possible.Name {
			return &ModuleEnum{
				TypeDef: e.TypeDef,
				Name:    possible.Name,
			}, nil
		}
	}
	// XXX: this doesn't seem neccessary...
	// for _, possible := range e.TypeDef.Members {
	// 	if val == possible.Value {
	// 		return &ModuleEnum{
	// 			TypeDef: e.TypeDef,
	// 			Name:    possible.Name,
	// 		}, nil
	// 	}
	// }
	return nil, fmt.Errorf("invalid enum value %q", val)
}

func (e *ModuleEnum) getTypeDef() *EnumMemberTypeDef {
	for _, possible := range e.TypeDef.Members {
		if possible.Name == e.Name {
			return possible
		}
	}
	return nil
}

func (e *ModuleEnum) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.Name)
}
