package templates

import (
	"github.com/dagger/dagger/codegen/generator"
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
	if alias, ok := generator.CustomScalar[refName]; ok && input {
		representation += alias
	} else {
		representation += refName
	}

	return representation
}

func (f *FormatTypeFunc) FormatKindObject(representation string, refName string) string {
	name := refName
	if name == generator.QueryStructName {
		name = generator.QueryStructClientName
	}

	representation += formatName(name)
	return representation
}

func (f *FormatTypeFunc) FormatKindInputObject(representation string, refName string) string {
	representation += formatName(refName)
	return representation
}
