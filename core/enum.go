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

var _ ModType = &ModuleEnumType{}

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

func (m *ModuleEnumType) ConvertFromSDKResult(ctx context.Context, value any) (dagql.AnyResult, error) {
	if value == nil {
		slog.Warn("%T.ConvertFromSDKResult: got nil value", m)
		return nil, nil
	}
	if enum, ok := value.(*ModuleEnum); ok {
		value = enum.Name
	}

	switch value := value.(type) {
	case string:
		enum, err := m.getEnum(ctx)
		if err != nil {
			return nil, fmt.Errorf("%T.ConvertFromSDKResult: failed to get enum: %w", m, err)
		}
		val, err := enum.DecodeInput(value)
		if err != nil {
			return nil, fmt.Errorf("%T.ConvertFromSDKResult: invalid enum value %q for %q: %w", m, value, m.typeDef.Name, err)
		}

		return dagql.NewResultForCurrentID(ctx, val)
	default:
		return nil, fmt.Errorf("unexpected result value type %T for enum %q", value, m.typeDef.Name)
	}
}

func (m *ModuleEnumType) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	if value == nil {
		return nil, nil
	}
	var input any = value
	if enum, ok := value.(*ModuleEnum); ok {
		input = enum.Name
	}

	base, err := m.getEnum(ctx)
	if err != nil {
		return nil, fmt.Errorf("%T.ConvertFromSDKResult: failed to get enum: %w", m, err)
	}

	decoder := *base
	decoder.Local = false
	result, err := decoder.DecodeInput(input)
	if err != nil {
		return nil, err
	}
	enum := result.(*ModuleEnum)
	if base.Local {
		return enum.memberTypedef().OriginalName, nil
	}
	return enum.memberTypedef().Name, nil
}

func (m *ModuleEnumType) CollectCoreIDs(ctx context.Context, value dagql.AnyResult, ids map[digest.Digest]*resource.ID) error {
	return nil
}

func (m *ModuleEnumType) getEnum(ctx context.Context) (*ModuleEnum, error) {
	// Check the dependencies
	srv, err := m.mod.Deps.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("%T.getDecoder: failed to get schema: %w", m, err)
	}

	scalar, ok := srv.ScalarType(m.typeDef.Name)
	if ok {
		enum, ok := scalar.(*ModuleEnum)
		if !ok {
			return nil, fmt.Errorf("%T.getDecoder: incorrect type %T for scalar", m, scalar)
		}
		return enum, nil
	}

	// If not check if the enum is part of its own module
	for _, enumTypeDef := range m.mod.EnumDefs {
		if enumTypeDef.AsEnum.Value.Name == m.typeDef.Name {
			return &ModuleEnum{TypeDef: enumTypeDef.AsEnum.Value, Local: true}, nil
		}
	}

	return nil, fmt.Errorf("%T.getDecoder: failed to get enum type %q", m, m.typeDef.Name)
}

type ModuleEnum struct {
	TypeDef *EnumTypeDef
	Name    string

	// Local marks this enum value as local to the module that declares its
	// typedef. This is so that when converting it to/from it's own module we
	// can use its OriginalName, but when converting it for other modules, we
	// use the declared Name.
	Local bool
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

func (e *ModuleEnum) TypeDefinition(view dagql.View) *ast.Definition {
	def := &ast.Definition{
		Kind:        ast.Enum,
		Name:        e.TypeName(),
		EnumValues:  e.PossibleValues(),
		Description: e.TypeDescription(),
	}
	if e.TypeDef.SourceMap.Valid {
		def.Directives = append(def.Directives, e.TypeDef.SourceMap.Value.TypeDirective())
	}
	return def
}

func (e *ModuleEnum) PossibleValues() ast.EnumValueList {
	var values ast.EnumValueList
	for _, val := range e.TypeDef.Members {
		name := val.Name
		if e.Local && val.OriginalName != "" {
			name = val.OriginalName
		}
		def := &ast.EnumValueDefinition{
			Name:        name,
			Description: val.Description,
			Directives:  val.EnumValueDirectives(),
		}
		if val.SourceMap.Valid {
			def.Directives = append(def.Directives, val.SourceMap.Value.TypeDirective())
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
	v, err := (&dagql.EnumValueName{Enum: e.TypeName()}).DecodeInput(val)
	if err != nil {
		return nil, err
	}
	return e.Lookup(v.(*dagql.EnumValueName).Name)
}

func (e *ModuleEnum) Lookup(val string) (dagql.Input, error) {
	if len(e.TypeDef.Members) == 0 {
		// this is a fairly good indication that something wrong has happened internally
		return nil, fmt.Errorf("enum %s has no members", e.TypeName())
	}
	for _, possible := range e.TypeDef.Members {
		name := possible.Name
		if e.Local && possible.OriginalName != "" {
			name = possible.OriginalName
		}
		if val == name {
			return &ModuleEnum{
				TypeDef: e.TypeDef,
				Name:    possible.Name,
			}, nil
		}
	}

	return nil, fmt.Errorf("invalid enum member %q for %s", val, e.TypeName())
}

func (e *ModuleEnum) memberTypedef() *EnumMemberTypeDef {
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
