package templates

import (
	"strings"

	"github.com/dagger/dagger/cmd/codegen/generator"
)

// FormatTypeFunc is an implementation of generator.FormatTypeFuncs interface
// to format GraphQL type into Ruby.
type FormatTypeFunc struct {
	scope string
}

func (f *FormatTypeFunc) WithScope(scope string) generator.FormatTypeFuncs {
	if scope != "" {
		scope += "."
	}
	clone := *f
	clone.scope = scope
	return &clone
}

func (f *FormatTypeFunc) FormatKindList(representation string) string {
	return "T::Array[" + representation + "]"
}

func (f *FormatTypeFunc) FormatKindScalarString(representation string) string {
	representation += "String"
	return representation
}

func (f *FormatTypeFunc) FormatKindScalarInt(representation string) string {
	representation += "Integer"
	return representation
}

func (f *FormatTypeFunc) FormatKindScalarFloat(representation string) string {
	representation += "Float"
	return representation
}

func (f *FormatTypeFunc) FormatKindScalarBoolean(representation string) string {
	representation += "T::Boolean"
	return representation
}

func (f *FormatTypeFunc) FormatKindScalarDefault(representation string, refName string, input bool) string {
	if obj, rest, ok := strings.Cut(refName, "ID"); input && ok && rest == "" {
		// map e.g. FooID to Foo
		representation += f.scope + formatName(obj)
	} else {
		representation += f.scope + refName
	}

	return representation
}

func (f *FormatTypeFunc) FormatKindObject(representation string, refName string, input bool) string {
	name := refName
	if name == generator.QueryStructName {
		name = generator.QueryStructClientName
	}

	representation += f.scope + formatName(name)
	return representation
}

func (f *FormatTypeFunc) FormatKindInputObject(representation string, refName string, input bool) string {
	representation += f.scope + formatName(refName)
	return representation
}

func (f *FormatTypeFunc) FormatKindEnum(representation string, refName string) string {
	representation += f.scope + refName
	return representation
}
