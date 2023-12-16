package introspection

import (
	"context"
	"fmt"

	"github.com/99designs/gqlgen/graphql/introspection"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql"
	"github.com/vito/dagql/idproto"
)

func Install[T dagql.Typed](srv *dagql.Server) {
	dagql.Fields[T]{
		dagql.Func("__schema", func(ctx context.Context, self T, args struct{}) (Schema, error) {
			return WrapSchema(srv.Schema()), nil
		}),
		dagql.Func("__type", func(ctx context.Context, self T, args struct {
			Name dagql.String
		}) (Type, error) {
			def, ok := srv.Schema().Types[args.Name.Value]
			if !ok {
				return Type{}, fmt.Errorf("unknown type: %q", args.Name)
			}
			return WrapTypeFromDef(srv.Schema(), def), nil
		}),
	}.Install(srv)

	TypeKinds.Install(srv)

	DirectiveLocations.Install(srv)

	dagql.Fields[Schema]{
		dagql.Func("queryType", func(ctx context.Context, self Schema, args struct{}) (Type, error) {
			return NewType(*self.QueryType()), nil
		}),
		dagql.Func("mutationType", func(ctx context.Context, self Schema, args struct{}) (dagql.Nullable[Type], error) {
			if self.MutationType() == nil {
				return dagql.Null[Type](), nil
			}
			return dagql.NonNull(NewType(*self.MutationType())), nil
		}),
		dagql.Func("subscriptionType", func(ctx context.Context, self Schema, args struct{}) (dagql.Nullable[Type], error) {
			if self.SubscriptionType() == nil {
				return dagql.Null[Type](), nil
			}
			return dagql.NonNull(NewType(*self.SubscriptionType())), nil
		}),
		dagql.Func("types", func(ctx context.Context, self Schema, args struct{}) (dagql.Array[Type], error) {
			var types []Type
			for _, def := range self.Types() {
				types = append(types, NewType(def))
			}
			return types, nil
		}),
		dagql.Func("directives", func(ctx context.Context, self Schema, args struct{}) (dagql.Array[Directive], error) {
			var directives []Directive
			for _, dir := range self.Directives() {
				directives = append(directives, NewDirective(dir))
			}
			return directives, nil
		}),
	}.Install(srv)

	dagql.Fields[Type]{
		dagql.Func("name", func(ctx context.Context, self Type, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.Name() == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.Name())), nil
			}
		}),
		dagql.Func("kind", func(ctx context.Context, self Type, args struct{}) (TypeKind, error) {
			return TypeKinds.Lookup(self.Kind())
		}),
		dagql.Func("description", func(ctx context.Context, self Type, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.Description() == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.Description())), nil
			}
		}),
		dagql.Func("fields", func(ctx context.Context, self Type, args struct {
			IncludeDeprecated dagql.Boolean `default:"false"`
		}) (dagql.Array[Field], error) {
			var fields dagql.Array[Field]
			for _, field := range self.Fields(args.IncludeDeprecated.Value) {
				fields = append(fields, NewField(field))
			}
			return fields, nil
		}),
		dagql.Func("inputFields", func(ctx context.Context, self Type, _ struct{}) (dagql.Array[InputValue], error) {
			var args []InputValue
			for _, arg := range self.InputFields() {
				args = append(args, NewInputValue(arg))
			}
			return args, nil
		}),
		dagql.Func("interfaces", func(ctx context.Context, self Type, args struct{}) (dagql.Array[Type], error) {
			var interfaces []Type
			for _, iface := range self.Interfaces() {
				interfaces = append(interfaces, NewType(iface))
			}
			return interfaces, nil
		}),
		dagql.Func("possibleTypes", func(ctx context.Context, self Type, args struct{}) (dagql.Array[Type], error) {
			var possibleTypes []Type
			for _, iface := range self.PossibleTypes() {
				possibleTypes = append(possibleTypes, NewType(iface))
			}
			return possibleTypes, nil
		}),
		dagql.Func("enumValues", func(ctx context.Context, self Type, args struct {
			IncludeDeprecated dagql.Boolean `default:"false"`
		}) (dagql.Array[EnumValue], error) {
			var values []EnumValue
			for _, val := range self.EnumValues(args.IncludeDeprecated.Value) {
				values = append(values, NewEnumValue(val))
			}
			return values, nil
		}),
		dagql.Func("ofType", func(ctx context.Context, self Type, args struct{}) (dagql.Nullable[Type], error) {
			if self.OfType() == nil {
				return dagql.Null[Type](), nil
			}
			return dagql.NonNull(NewType(*self.OfType())), nil
		}),
	}.Install(srv)

	dagql.Fields[Directive]{
		dagql.Func("name", func(ctx context.Context, self Directive, args struct{}) (dagql.String, error) {
			return dagql.NewString(self.Name), nil
		}),
		dagql.Func("description", func(ctx context.Context, self Directive, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.Description() == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.Description())), nil
			}
		}),
		dagql.Func("locations", func(ctx context.Context, self Directive, args struct{}) (dagql.Array[DirectiveLocation], error) {
			var locations []DirectiveLocation
			for _, loc := range self.Locations {
				enum, err := DirectiveLocations.Lookup(loc)
				if err != nil {
					return nil, err
				}
				locations = append(locations, enum)
			}
			return locations, nil
		}),
		dagql.Func("args", func(ctx context.Context, self Directive, _ struct{}) (dagql.Array[InputValue], error) {
			var args []InputValue
			for _, arg := range self.Args {
				args = append(args, NewInputValue(arg))
			}
			return args, nil
		}),
	}.Install(srv)

	dagql.Fields[Field]{
		dagql.Func("name", func(ctx context.Context, self Field, args struct{}) (dagql.String, error) {
			return dagql.NewString(self.Name), nil
		}),
		dagql.Func("description", func(ctx context.Context, self Field, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.Description() == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.Description())), nil
			}
		}),
		dagql.Func("args", func(ctx context.Context, self Field, _ struct{}) (dagql.Array[InputValue], error) {
			var args []InputValue
			for _, arg := range self.Args {
				args = append(args, NewInputValue(arg))
			}
			return args, nil
		}),
		dagql.Func("type", func(ctx context.Context, self Field, args struct{}) (Type, error) {
			return NewType(*self.Field.Type), nil
		}),
		dagql.Func("isDeprecated", func(ctx context.Context, self Field, args struct{}) (dagql.Boolean, error) {
			return dagql.NewBoolean(self.IsDeprecated()), nil
		}),
		dagql.Func("deprecationReason", func(ctx context.Context, self Field, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.DeprecationReason() == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.DeprecationReason())), nil
			}
		}),
	}.Install(srv)

	dagql.Fields[InputValue]{
		dagql.Func("name", func(ctx context.Context, self InputValue, args struct{}) (dagql.String, error) {
			return dagql.NewString(self.Name), nil
		}),
		dagql.Func("description", func(ctx context.Context, self InputValue, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.Description() == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.Description())), nil
			}
		}),
		dagql.Func("type", func(ctx context.Context, self InputValue, args struct{}) (Type, error) {
			return NewType(*self.InputValue.Type), nil
		}),
		dagql.Func("defaultValue", func(ctx context.Context, self InputValue, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.DefaultValue == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.DefaultValue)), nil
			}
		}),
	}.Install(srv)

	dagql.Fields[EnumValue]{
		dagql.Func("name", func(ctx context.Context, self EnumValue, args struct{}) (dagql.String, error) {
			return dagql.NewString(self.Name), nil
		}),
		dagql.Func("description", func(ctx context.Context, self EnumValue, args struct{}) (dagql.Optional[dagql.String], error) {
			if self.Description() == nil {
				return dagql.NoOpt[dagql.String](), nil
			} else {
				return dagql.Opt(dagql.NewString(*self.Description())), nil
			}
		}),
		dagql.Func("isDeprecated", func(ctx context.Context, self EnumValue, args struct{}) (dagql.Boolean, error) {
			return dagql.NewBoolean(self.IsDeprecated()), nil
		}),
		dagql.Func("deprecationReason", func(ctx context.Context, self EnumValue, args struct{}) (dagql.Optional[dagql.String], error) {
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

var _ dagql.Typed = Schema{}

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

var _ dagql.Typed = Type{}

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

var _ dagql.Typed = Directive{}

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

var _ dagql.Typed = InputValue{}

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

var _ dagql.Typed = Field{}

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

var _ dagql.Typed = EnumValue{}

func (s EnumValue) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__EnumValue",
		NonNull:   true,
	}
}

type TypeKind string

var TypeKinds = dagql.NewEnum[TypeKind](
	"SCALAR",
	"OBJECT",
	"INTERFACE",
	"UNION",
	"ENUM",
	"INPUT_OBJECT",
	"LIST",
	"NON_NULL",
)

func (k TypeKind) Decoder() dagql.InputDecoder {
	return TypeKinds
}

func (k TypeKind) ToLiteral() *idproto.Literal {
	return TypeKinds.Literal(k)
}

var _ dagql.Typed = TypeKind("")

func (k TypeKind) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__TypeKind",
		NonNull:   true,
	}
}

type DirectiveLocation string

var DirectiveLocations = dagql.NewEnum[DirectiveLocation](
	"QUERY",
	"MUTATION",
	"SUBSCRIPTION",
	"FIELD",
	"FRAGMENT_DEFINITION",
	"FRAGMENT_SPREAD",
	"INLINE_FRAGMENT",
	"VARIABLE_DEFINITION",
	"SCHEMA",
	"SCALAR",
	"OBJECT",
	"FIELD_DEFINITION",
	"ARGUMENT_DEFINITION",
	"INTERFACE",
	"UNION",
	"ENUM",
	"ENUM_VALUE",
	"INPUT_OBJECT",
	"INPUT_FIELD_DEFINITION",
)

func (k DirectiveLocation) Decoder() dagql.InputDecoder {
	return DirectiveLocations
}

func (k DirectiveLocation) ToLiteral() *idproto.Literal {
	return DirectiveLocations.Literal(k)
}

var _ dagql.Typed = DirectiveLocation("")

func (k DirectiveLocation) Type() *ast.Type {
	return &ast.Type{
		NamedType: "__DirectiveLocation",
		NonNull:   true,
	}
}
