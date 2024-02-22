package templates

import (
	"strings"

	"github.com/dagger/dagger/cmd/codegen/generator"
)

// FormatTypeFunc is an implementation of generator.FormatTypeFuncs interface
// to format GraphQL type into Golang.
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
	representation = "[]" + representation
	return representation
}

func (f *FormatTypeFunc) FormatKindScalarString(representation string) string {
	representation += "string"
	return representation
}

func (f *FormatTypeFunc) FormatKindScalarInt(representation string) string {
	representation += "int"
	return representation
}

func (f *FormatTypeFunc) FormatKindScalarFloat(representation string) string {
	representation += "float"
	return representation
}

func (f *FormatTypeFunc) FormatKindScalarBoolean(representation string) string {
	representation += "bool"
	return representation
}

func (f *FormatTypeFunc) FormatKindScalarDefault(representation string, refName string, input bool) string {
	if obj, ok := strings.CutSuffix(refName, "ID"); input && ok {
		representation += "*" + f.scope + formatName(obj)
	} else {
		representation += f.scope + formatName(refName)
	}

	return representation
}

func (f *FormatTypeFunc) FormatKindObject(representation string, refName string, input bool) string {
	representation += f.scope + formatName(refName)
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
