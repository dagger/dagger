package templates

import (
	"strings"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
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
	representation += "float64"
	return representation
}

func (f *FormatTypeFunc) FormatKindScalarBoolean(representation string) string {
	representation += "bool"
	return representation
}

func (f *FormatTypeFunc) FormatKindScalarDefault(representation string, refName string, input bool) string {
	if obj, ok := strings.CutSuffix(refName, "ID"); input && ok && obj != "" {
		// Check if the object type is an interface. If so, the Go type
		// is a Go interface (not a struct), so we don't add a pointer prefix.
		if schema := generator.GetSchema(); schema != nil {
			if schemaType := schema.Types.Get(obj); schemaType != nil && schemaType.Kind == introspection.TypeKindInterface {
				representation += f.scope + formatName(obj)
				return representation
			}
		}
		representation += "*" + f.scope + formatName(obj)
	} else {
		representation += f.scope + formatName(refName)
	}

	return representation
}

// FormatKindScalarID formats an ID argument using the @expectedType directive.
// If expectedType is set, use the expected type name (as a pointer for objects,
// or as-is for interfaces). Otherwise fall back to the scalar name.
func (f *FormatTypeFunc) FormatKindScalarID(representation string, expectedType string) string {
	if expectedType == "" {
		// No expected type — use the raw ID type.
		representation += f.scope + formatName("ID")
		return representation
	}
	// Check if the expected type is an interface.
	if schema := generator.GetSchema(); schema != nil {
		if schemaType := schema.Types.Get(expectedType); schemaType != nil && schemaType.Kind == introspection.TypeKindInterface {
			representation += f.scope + formatName(expectedType)
			return representation
		}
	}
	representation += "*" + f.scope + formatName(expectedType)
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
