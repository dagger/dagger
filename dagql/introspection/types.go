package introspection

import (
	"context"
	"fmt"

	"github.com/99designs/gqlgen/graphql/introspection"
	"github.com/vito/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

func Install[T dagql.Typed](srv *dagql.Server) {
	dagql.Fields[T]{
		"__schema": dagql.Func(func(ctx context.Context, self T, args struct{}) (Schema, error) {
			return WrapSchema(srv.Schema()), nil
		}),
		"__type": dagql.Func(func(ctx context.Context, self T, args struct {
			Name dagql.String
		}) (Type, error) {
			def, ok := srv.Schema().Types[args.Name.Value]
			if !ok {
				return Type{}, fmt.Errorf("unknown type: %q", args.Name)
			}
			return WrapTypeFromDef(srv.Schema(), def), nil
		}),
	}.Install(srv)

	typeKind := dagql.EnumSpec{
		Name: "__TypeKind",
		Values: []*ast.EnumValueDefinition{
			{Name: "SCALAR"},
			{Name: "OBJECT"},
			{Name: "INTERFACE"},
			{Name: "UNION"},
			{Name: "ENUM"},
			{Name: "INPUT_OBJECT"},
			{Name: "LIST"},
			{Name: "NON_NULL"},
		},
	}
	typeKind.Install(srv)

	directiveLocation := dagql.EnumSpec{
		Name: "__DirectiveLocation",
		Values: []*ast.EnumValueDefinition{
			{Name: "QUERY"},
			{Name: "MUTATION"},
			{Name: "SUBSCRIPTION"},
			{Name: "FIELD"},
			{Name: "FRAGMENT_DEFINITION"},
			{Name: "FRAGMENT_SPREAD"},
			{Name: "INLINE_FRAGMENT"},
			{Name: "VARIABLE_DEFINITION"},
			{Name: "SCHEMA"},
			{Name: "SCALAR"},
			{Name: "OBJECT"},
			{Name: "FIELD_DEFINITION"},
			{Name: "ARGUMENT_DEFINITION"},
			{Name: "INTERFACE"},
			{Name: "UNION"},
			{Name: "ENUM"},
			{Name: "ENUM_VALUE"},
			{Name: "INPUT_OBJECT"},
			{Name: "INPUT_FIELD_DEFINITION"},
		},
	}
	directiveLocation.Install(srv)

	dagql.Fields[Schema]{
		"queryType": dagql.Func(func(ctx context.Context, self Schema, args struct{}) (Type, error) {
			return NewType(*self.QueryType()), nil
		}),
		"mutationType": dagql.Func(func(ctx context.Context, self Schema, args struct{}) (dagql.Optional[Type], error) {
			if self.MutationType() == nil {
				return dagql.Optional[Type]{}, nil
			}
			return dagql.Opt(NewType(*self.MutationType())), nil
		}),
		"subscriptionType": dagql.Func(func(ctx context.Context, self Schema, args struct{}) (dagql.Optional[Type], error) {
			if self.SubscriptionType() == nil {
				return dagql.Optional[Type]{}, nil
			}
			return dagql.Opt(NewType(*self.SubscriptionType())), nil
		}),
		"types": dagql.Func(func(ctx context.Context, self Schema, args struct{}) (dagql.Array[Type], error) {
			var types []Type
			for _, def := range self.Types() {
				types = append(types, NewType(def))
			}
			return types, nil
		}),
		"directives": dagql.Func(func(ctx context.Context, self Schema, args struct{}) (dagql.Array[Directive], error) {
			var directives []Directive
			for _, dir := range self.Directives() {
				directives = append(directives, NewDirective(dir))
			}
			return directives, nil
		}),
	}.Install(srv)

	dagql.Fields[Type]{
		"name": dagql.Func(func(ctx context.Context, self Type, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.Name() == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.Name())), nil
			}
		}),
		"kind": dagql.Func(func(ctx context.Context, self Type, args struct{}) (dagql.Enum, error) {
			return dagql.Enum{
				Enum:  typeKind.Type(),
				Value: self.Kind(),
			}, nil
		}),
		"description": dagql.Func(func(ctx context.Context, self Type, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.Description() == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.Description())), nil
			}
		}),
		"fields": dagql.Func(func(ctx context.Context, self Type, args struct {
			IncludeDeprecated dagql.Boolean `default:"false"`
		}) (dagql.Array[Field], error) {
			var fields dagql.Array[Field]
			for _, field := range self.Fields(args.IncludeDeprecated.Value) {
				fields = append(fields, NewField(field))
			}
			return fields, nil
		}),
		"inputFields": dagql.Func(func(ctx context.Context, self Type, _ struct{}) (dagql.Array[InputValue], error) {
			var args []InputValue
			for _, arg := range self.InputFields() {
				args = append(args, NewInputValue(arg))
			}
			return args, nil
		}),
		"interfaces": dagql.Func(func(ctx context.Context, self Type, args struct{}) (dagql.Array[Type], error) {
			var interfaces []Type
			for _, iface := range self.Interfaces() {
				interfaces = append(interfaces, NewType(iface))
			}
			return interfaces, nil
		}),
		"possibleTypes": dagql.Func(func(ctx context.Context, self Type, args struct{}) (dagql.Array[Type], error) {
			var possibleTypes []Type
			for _, iface := range self.PossibleTypes() {
				possibleTypes = append(possibleTypes, NewType(iface))
			}
			return possibleTypes, nil
		}),
		"enumValues": dagql.Func(func(ctx context.Context, self Type, args struct {
			IncludeDeprecated dagql.Boolean `default:"false"`
		}) (dagql.Array[EnumValue], error) {
			var values []EnumValue
			for _, val := range self.EnumValues(args.IncludeDeprecated.Value) {
				values = append(values, NewEnumValue(val))
			}
			return values, nil
		}),
		"ofType": dagql.Func(func(ctx context.Context, self Type, args struct{}) (dagql.Optional[Type], error) {
			if self.OfType() == nil {
				return dagql.Optional[Type]{}, nil
			}
			return dagql.Opt(NewType(*self.OfType())), nil
		}),
	}.Install(srv)

	dagql.Fields[Directive]{
		"name": dagql.Func(func(ctx context.Context, self Directive, args struct{}) (dagql.String, error) {
			return dagql.NewString(self.Name), nil
		}),
		"description": dagql.Func(func(ctx context.Context, self Directive, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.Description() == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.Description())), nil
			}
		}),
		"locations": dagql.Func(func(ctx context.Context, self Directive, args struct{}) (dagql.Array[dagql.Enum], error) {
			var locations []dagql.Enum
			for _, loc := range self.Locations {
				locations = append(locations, dagql.Enum{
					Enum:  directiveLocation.Type(),
					Value: loc,
				})
			}
			return locations, nil
		}),
		"args": dagql.Func(func(ctx context.Context, self Directive, _ struct{}) (dagql.Array[InputValue], error) {
			var args []InputValue
			for _, arg := range self.Args {
				args = append(args, NewInputValue(arg))
			}
			return args, nil
		}),
	}.Install(srv)

	dagql.Fields[Field]{
		"name": dagql.Func(func(ctx context.Context, self Field, args struct{}) (dagql.String, error) {
			return dagql.NewString(self.Name), nil
		}),
		"description": dagql.Func(func(ctx context.Context, self Field, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.Description() == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.Description())), nil
			}
		}),
		"args": dagql.Func(func(ctx context.Context, self Field, _ struct{}) (dagql.Array[InputValue], error) {
			var args []InputValue
			for _, arg := range self.Args {
				args = append(args, NewInputValue(arg))
			}
			return args, nil
		}),
		"type": dagql.Func(func(ctx context.Context, self Field, args struct{}) (Type, error) {
			return NewType(*self.Field.Type), nil
		}),
		"isDeprecated": dagql.Func(func(ctx context.Context, self Field, args struct{}) (dagql.Boolean, error) {
			return dagql.NewBoolean(self.IsDeprecated()), nil
		}),
		"deprecationReason": dagql.Func(func(ctx context.Context, self Field, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.DeprecationReason() == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.DeprecationReason())), nil
			}
		}),
	}.Install(srv)

	dagql.Fields[InputValue]{
		"name": dagql.Func(func(ctx context.Context, self InputValue, args struct{}) (dagql.String, error) {
			return dagql.NewString(self.Name), nil
		}),
		"description": dagql.Func(func(ctx context.Context, self InputValue, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.Description() == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.Description())), nil
			}
		}),
		"type": dagql.Func(func(ctx context.Context, self InputValue, args struct{}) (Type, error) {
			return NewType(*self.InputValue.Type), nil
		}),
		"defaultValue": dagql.Func(func(ctx context.Context, self InputValue, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.DefaultValue == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.DefaultValue)), nil
			}
		}),
	}.Install(srv)

	dagql.Fields[EnumValue]{
		"name": dagql.Func(func(ctx context.Context, self EnumValue, args struct{}) (dagql.String, error) {
			return dagql.NewString(self.Name), nil
		}),
		"description": dagql.Func(func(ctx context.Context, self EnumValue, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.Description() == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.Description())), nil
			}
		}),
		"isDeprecated": dagql.Func(func(ctx context.Context, self EnumValue, args struct{}) (dagql.Boolean, error) {
			return dagql.NewBoolean(self.IsDeprecated()), nil
		}),
		"deprecationReason": dagql.Func(func(ctx context.Context, self EnumValue, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.DeprecationReason() == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.DeprecationReason())), nil
			}
		}),
	}.Install(srv)
}

type Schema struct {
	*introspection.Schema
}

func WrapSchema(schema *ast.Schema) Schema {
	return Schema{introspection.WrapSchema(schema)}
}

func (s Schema) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__Schema",
		NonNull:   true,
	}
}

// workaround Type method conflict
type type_ = introspection.Type

type Type struct {
	*type_
}

func NewType(t introspection.Type) Type {
	return Type{&t}
}

func (s Type) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__Type",
		NonNull:   true,
	}
}

func WrapTypeFromDef(schema *ast.Schema, def *ast.Definition) Type {
	return Type{
		type_: (*type_)(introspection.WrapTypeFromDef(schema, def)),
	}
}

func WrapTypeFromType(schema *ast.Schema, typ *ast.Type) Type {
	return Type{
		type_: (*type_)(introspection.WrapTypeFromType(schema, typ)),
	}
}

type Directive struct {
	*introspection.Directive
}

func NewDirective(x introspection.Directive) Directive {
	return Directive{&x}
}

func (s Directive) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__Directive",
		NonNull:   true,
	}
}

type InputValue struct {
	*introspection.InputValue
}

func NewInputValue(x introspection.InputValue) InputValue {
	return InputValue{&x}
}

func (s InputValue) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__InputValue",
		NonNull:   true,
	}
}

type Field struct {
	*introspection.Field
}

func NewField(x introspection.Field) Field {
	return Field{&x}
}

func (s Field) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__Field",
		NonNull:   true,
	}
}

type EnumValue struct {
	*introspection.EnumValue
}

func NewEnumValue(x introspection.EnumValue) EnumValue {
	return EnumValue{&x}
}

func (s EnumValue) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__EnumValue",
		NonNull:   true,
	}
}
