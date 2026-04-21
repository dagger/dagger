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
	mod     dagql.ObjectResult[*Module]
}

var _ ModType = &ModuleEnumType{}

func (m *ModuleEnumType) SourceMod() Mod {
	if m.mod.Self() == nil {
		return nil
	}
	return NewUserMod(m.mod)
}

func (m *ModuleEnumType) TypeDef(ctx context.Context) (dagql.ObjectResult[*TypeDef], error) {
	var sourceMap dagql.Optional[dagql.ID[*SourceMap]]
	if m.typeDef.SourceMap.Valid {
		var err error
		sourceMap, err = OptionalResultIDInput(m.typeDef.SourceMap.Value)
		if err != nil {
			return dagql.ObjectResult[*TypeDef]{}, err
		}
	}
	return SelectTypeDef(ctx, dagql.Selector{
		Field: "withEnum",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String(m.typeDef.Name)},
			{Name: "description", Value: dagql.String(m.typeDef.Description)},
			{Name: "sourceMap", Value: sourceMap},
			{Name: "sourceModuleName", Value: OptSourceModuleName(m.typeDef.SourceModuleName)},
		},
	})
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

		return dagql.NewResultForCurrentCall(ctx, val)
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

func (m *ModuleEnumType) CollectContent(ctx context.Context, value dagql.AnyResult, content *CollectedContent) error {
	if value == nil {
		return content.CollectJSONable(nil)
	}
	return content.CollectJSONable(value.Unwrap())
}

func (m *ModuleEnumType) getEnum(ctx context.Context) (*ModuleEnum, error) {
	// Check the dependencies
	srv, err := m.mod.Self().Deps.Schema(ctx)
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
	for _, enumTypeDef := range m.mod.Self().EnumDefs {
		if enumTypeDef.Self().AsEnum.Value.Self().Name == m.typeDef.Name {
			return &ModuleEnum{TypeDef: enumTypeDef.Self().AsEnum.Value.Self(), Local: true}, nil
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

func (e *ModuleEnum) TypeDefinition(view call.View) *ast.Definition {
	def := &ast.Definition{
		Kind:        ast.Enum,
		Name:        e.TypeName(),
		EnumValues:  e.PossibleValues(),
		Description: e.TypeDescription(),
	}
	if e.TypeDef.SourceMap.Valid {
		def.Directives = append(def.Directives, e.TypeDef.SourceMap.Value.Self().TypeDirective())
	}
	return def
}

func (e *ModuleEnum) PossibleValues() ast.EnumValueList {
	var values ast.EnumValueList
	for _, val := range e.TypeDef.Members {
		member := val.Self()
		name := member.Name
		if e.Local && member.OriginalName != "" {
			name = member.OriginalName
		}
		def := &ast.EnumValueDefinition{
			Name:        name,
			Description: member.Description,
			Directives:  member.EnumValueDirectives(),
		}
		if member.SourceMap.Valid {
			def.Directives = append(def.Directives, member.SourceMap.Value.Self().TypeDirective())
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
		member := possible.Self()
		name := member.Name
		if e.Local && member.OriginalName != "" {
			name = member.OriginalName
		}
		if val == name {
			return &ModuleEnum{
				TypeDef: e.TypeDef,
				Name:    member.Name,
			}, nil
		}
	}

	return nil, fmt.Errorf("invalid enum member %q for %s", val, e.TypeName())
}

func (e *ModuleEnum) memberTypedef() *EnumMemberTypeDef {
	for _, possible := range e.TypeDef.Members {
		if possible.Self().Name == e.Name {
			return possible.Self()
		}
	}
	return nil
}

func (e *ModuleEnum) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.Name)
}
