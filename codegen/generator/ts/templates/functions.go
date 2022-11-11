package templates

import (
	"strings"
	"text/template"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/codegen/introspection"
)

var (
	FuncMap = template.FuncMap{
		"CommentToLines":   CommentToLines,
		"FormatInputType":  FormatInputType,
		"FormatOutputType": FormatOutputType,
		"FormatName":       FormatName,
		"HasPrefix":        strings.HasPrefix,
		"PascalCase":       PascalCase,
		"IsArgOptional":    IsArgOptional,
		"IsCustomScalar":   IsCustomScalar,
		"Solve":            Solve,
		"Subtract":         Subtract,
	}
)

// PascalCase change a type name into PascalCase
func PascalCase(name string) string {
	return strcase.ToCamel(name)
}

// Solve checks if a field is solveable.
func Solve(field introspection.Field) bool {
	if field.TypeRef == nil {
		return false
	}
	return field.TypeRef.IsScalar() || field.TypeRef.IsList()
}

// Subtract subtract integer a with integer b.
func Subtract(a, b int) int {
	return a - b
}

// CommentToLines split a string by line breaks to be used in comments
func CommentToLines(s string) []string {
	split := strings.Split(s, "\n")
	return split
}

// formatType formats a GraphQL type into Go
// Example: `String` -> `string`
// TODO: maybe delete and only use formatType?
func FormatInputType(r *introspection.TypeRef) string {
	return formatType(r)
}

// TODO: maybe delete and only use formatType?
func FormatOutputType(r *introspection.TypeRef) string {
	return formatType(r)
}

// IsCustomScalar checks if the type is actually custom.
func IsCustomScalar(t *introspection.Type) bool {
	switch introspection.Scalar(t.Name) {
	case introspection.ScalarString, introspection.ScalarInt, introspection.ScalarFloat, introspection.ScalarBoolean:
		return false
	default:
		return true && t.Kind == introspection.TypeKindScalar
	}
}

// formatType formats a GraphQL type into TypeScript
// Example: `String` -> `string`
func formatType(r *introspection.TypeRef) (representation string) {
	for ref := r; ref != nil; ref = ref.OfType {
		switch ref.Kind {
		case introspection.TypeKindList:
			// add [] as suffix to the type
			defer func() {
				representation += "[]"
			}()
		case introspection.TypeKindScalar:
			switch introspection.Scalar(ref.Name) {
			case introspection.ScalarString:
				representation += "string"
				return representation
			case introspection.ScalarInt, introspection.ScalarFloat:
				representation += "number"
				return representation
			case introspection.ScalarBoolean:
				representation += "boolean"
				return representation
			default:
				// Custom scalar
				representation += ref.Name
				return representation
			}
		case introspection.TypeKindObject:
			representation += FormatName(ref.Name)
			return representation
		case introspection.TypeKindInputObject:
			representation += FormatName(ref.Name)
			return representation
		}
	}

	panic(r)
}

// FormatName formats a GraphQL name (e.g. object, field, arg) into a TS equivalent
// Example: `fooId` -> `FooId`
func FormatName(s string) string {
	return s
}

// IsArgOptional checks if some arg are optional.
// They are, if all of there InputValues are optional.
func IsArgOptional(values introspection.InputValues) bool {
	for _, v := range values {
		if v.TypeRef != nil && !v.TypeRef.IsOptional() {
			return false
		}
	}
	return true
}
