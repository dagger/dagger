package templates

import (
	"strings"

	"dagger.io/dagger/codegen/generator"
)

// FormatTypeFunc is an implementation of generator.FormatTypeFuncs interface
// to format GraphQL type into Typescript.
type FormatTypeFunc struct{}

func (f *FormatTypeFunc) FormatKindList(representation string) string {
	representation += "[]"
	return representation
}

func (f *FormatTypeFunc) FormatKindScalarString(representation string) string {
	representation += "string"
	return representation
}

func (f *FormatTypeFunc) FormatKindScalarInt(representation string) string {
	representation += "number"
	return representation
}

func (f *FormatTypeFunc) FormatKindScalarFloat(representation string) string {
	representation += "number"
	return representation
}

func (f *FormatTypeFunc) FormatKindScalarBoolean(representation string) string {
	representation += "boolean"
	return representation
}

func (f *FormatTypeFunc) FormatKindScalarDefault(representation string, refName string, input bool) string {
	if obj, rest, ok := strings.Cut(refName, "ID"); input && ok && rest == "" {
		// map e.g. FooID to Foo
		representation += formatName(obj)
	} else {
		representation += refName
	}

	return representation
}

func (f *FormatTypeFunc) FormatKindObject(representation string, refName string, input bool) string {
	name := refName
	if name == generator.QueryStructName {
		name = generator.QueryStructClientName
	}

	representation += formatName(name)
	return representation
}

func (f *FormatTypeFunc) FormatKindInputObject(representation string, refName string, input bool) string {
	representation += formatName(refName)
	return representation
}

func (f *FormatTypeFunc) FormatKindEnum(representation string, refName string) string {
	representation += refName
	return representation
}
