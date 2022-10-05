package templates

import (
	"fmt"
	"strings"
	"text/template"

	"go.dagger.io/dagger/codegen/introspection"
)

var (
	funcMap = template.FuncMap{
		"Comment":                comment,
		"FormatType":             formatType,
		"FormatName":             formatName,
		"FormatTypeAndFieldName": formatTypeAndFieldName,
		"FieldOptionsStructName": fieldOptionsStructName,
		"FieldOptionHandlerName": fieldOptionHandlerName,
		"FieldFunction":          fieldFunction,
	}
)

// comments out a string
// Example: `hello\nworld` -> `// hello\n// world\n`
func comment(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "// " + l
	}
	return strings.Join(lines, "\n")
}

// formatType formats a GraphQL type into Go
// Example: `String` -> `string`
func formatType(r *introspection.TypeRef) string {
	var representation string
	for ref := r; ref != nil; ref = ref.OfType {
		switch ref.Kind {
		case introspection.TypeKindList:
			representation += "[]"
		case introspection.TypeKindScalar:
			switch introspection.Scalar(ref.Name) {
			case introspection.ScalarString:
				representation += "string"
				return representation
			case introspection.ScalarInt:
				representation += "int"
				return representation
			case introspection.ScalarBoolean:
				representation += "bool"
				return representation
			case introspection.ScalarFloat:
				representation += "float"
				return representation
			default:
				// Custom scalar
				return ref.Name
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

// formatName formats a GraphQL name (e.g. object, field, arg) into a Go equivalent
// Example: `fooId` -> `FooID`
func formatName(s string) string {
	if len(s) > 0 {
		s = strings.ToUpper(string(s[0])) + s[1:]
	}
	return lintName(s)
}

// formatTypeAndFieldName formats a GraphQL field into a Go equivalent in the form of `<Type Name><FieldName>`.
// Example: `foo` -> `TypeFoo`
func formatTypeAndFieldName(f introspection.Field) string {
	return formatName(f.ParentObject.Name) + formatName(f.Name)
}

// fieldOptionsStructName returns the options struct name for a given field
func fieldOptionsStructName(f introspection.Field) string {
	return formatTypeAndFieldName(f) + "Options"
}

// fieldOptionHandlerName returns the option handler name for a given field
func fieldOptionHandlerName(f introspection.Field) string {
	return formatTypeAndFieldName(f) + "Option"
}

// fieldFunction converts a field into a function signature
// Example: `contents: String!` -> `func (r *File) Contents(ctx context.Context) (string, error)`
func fieldFunction(f introspection.Field) string {
	signature := fmt.Sprintf(`func (r *%s) %s`,
		formatName(f.ParentObject.Name), formatName(f.Name))

	// Generate arguments
	args := []string{}
	if f.TypeRef.IsScalar() {
		args = append(args, "ctx context.Context")
	}
	for _, arg := range f.Args {
		if !arg.TypeRef.IsOptional() {
			args = append(args, fmt.Sprintf("%s %s", arg.Name, formatType(arg.TypeRef)))
		}
	}
	// Options (e.g. DirectoryContentsOptions -> <Object><Field>Options)
	if f.Args.HasOptionals() {
		args = append(
			args,
			fmt.Sprintf("options ...%sOption", formatTypeAndFieldName(f)),
		)
	}
	signature += "(" + strings.Join(args, ", ") + ")"

	retType := ""
	if f.TypeRef.IsScalar() {
		retType = fmt.Sprintf("(%s, error)", formatType(f.TypeRef))
	} else {
		retType = "*" + formatType(f.TypeRef)
	}
	signature += " " + retType

	return signature
}
