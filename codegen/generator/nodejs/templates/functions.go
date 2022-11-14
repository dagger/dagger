package templates

import (
	"strings"
	"text/template"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/codegen/introspection"
)

var (
	funcMap = template.FuncMap{
		"CommentToLines":   commentToLines,
		"FormatInputType":  formatInputType,
		"FormatOutputType": formatOutputType,
		"FormatName":       formatName,
		"HasPrefix":        strings.HasPrefix,
		"PascalCase":       pascalCase,
		"IsArgOptional":    isArgOptional,
		"IsCustomScalar":   isCustomScalar,
		"Solve":            solve,
		"Subtract":         subtract,
	}
)

// pascalCase change a type name into pascalCase
func pascalCase(name string) string {
	return strcase.ToCamel(name)
}

// solve checks if a field is solveable.
func solve(field introspection.Field) bool {
	if field.TypeRef == nil {
		return false
	}
	return field.TypeRef.IsScalar() || field.TypeRef.IsList()
}

// subtract subtract integer a with integer b.
func subtract(a, b int) int {
	return a - b
}

// commentToLines split a string by line breaks to be used in comments
func commentToLines(s string) []string {
	split := strings.Split(s, "\n")
	return split
}

// formatType formats a GraphQL type into Go
// Example: `String` -> `string`
// TODO: maybe delete and only use formatType?
func formatInputType(r *introspection.TypeRef) string {
	return formatType(r)
}

// TODO: maybe delete and only use formatType?
func formatOutputType(r *introspection.TypeRef) string {
	return formatType(r)
}

// isCustomScalar checks if the type is actually custom.
func isCustomScalar(t *introspection.Type) bool {
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
			representation += formatName(ref.Name)
			return representation
		case introspection.TypeKindInputObject:
			representation += formatName(ref.Name)
			return representation
		}
	}

	panic(r)
}

// formatName formats a GraphQL name (e.g. object, field, arg) into a TS equivalent
func formatName(s string) string {
	return s
}

// isArgOptional checks if some arg are optional.
// They are, if all of there InputValues are optional.
func isArgOptional(values introspection.InputValues) bool {
	for _, v := range values {
		if v.TypeRef != nil && !v.TypeRef.IsOptional() {
			return false
		}
	}
	return true
}
