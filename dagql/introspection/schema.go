package introspection

import (
	"github.com/99designs/gqlgen/graphql/introspection"
	"github.com/vektah/gqlparser/v2/ast"
)

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
