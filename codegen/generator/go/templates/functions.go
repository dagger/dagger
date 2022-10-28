package templates

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/dagger/dagger/codegen/introspection"
)

var (
	funcMap = template.FuncMap{
		"Comment":                comment,
		"FormatInputType":        formatInputType,
		"FormatOutputType":       formatOutputType,
		"FormatName":             formatName,
		"FieldOptionsStructName": fieldOptionsStructName,
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
func formatInputType(r *introspection.TypeRef) string {
	return formatType(r, true)
}

func formatOutputType(r *introspection.TypeRef) string {
	return formatType(r, false)
}

// formatType formats a GraphQL type into Go
// Example: `String` -> `string`
func formatType(r *introspection.TypeRef, input bool) string {
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

				// When used as an input, we're going to use objects rather than ID scalars (e.g. `*Container` rather than `ContainerID`)
				// FIXME: do this dynamically rather than a hardcoded map.
				rewrite := map[string]string{
					"ContainerID": "Container",
					"FileID":      "File",
					"DirectoryID": "Directory",
					"SecretID":    "Secret",
					"CacheID":     "CacheVolume",
				}
				if alias, ok := rewrite[ref.Name]; ok && input {
					representation += "*" + alias
				} else {
					representation += ref.Name
				}
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

// formatName formats a GraphQL name (e.g. object, field, arg) into a Go equivalent
// Example: `fooId` -> `FooID`
func formatName(s string) string {
	if len(s) > 0 {
		s = strings.ToUpper(string(s[0])) + s[1:]
	}
	return lintName(s)
}

// fieldOptionsStructName returns the options struct name for a given field
func fieldOptionsStructName(f introspection.Field) string {
	// Exception: `Query` option structs are not prefixed by `Query`.
	// This is just so that they're nicer to work with, e.g.
	// `ContainerOpts` rather than `QueryContainerOpts`
	// The structure name will not clash with others since everybody else
	// is prefixed by object name.
	if f.ParentObject.Name == "Query" {
		return formatName(f.Name) + "Opts"
	}
	return formatName(f.ParentObject.Name) + formatName(f.Name) + "Opts"
}

// fieldFunction converts a field into a function signature
// Example: `contents: String!` -> `func (r *File) Contents(ctx context.Context) (string, error)`
func fieldFunction(f introspection.Field) string {
	signature := fmt.Sprintf(`func (r *%s) %s`,
		formatName(f.ParentObject.Name), formatName(f.Name))

	// Generate arguments
	args := []string{}
	if f.TypeRef.IsScalar() || f.TypeRef.IsList() {
		args = append(args, "ctx context.Context")
	}
	for _, arg := range f.Args {
		if arg.TypeRef.IsOptional() {
			continue
		}

		// FIXME: For top-level queries (e.g. File, Directory) if the field is named `id` then keep it as a
		// scalar (DirectoryID) rather than an object (*Directory).
		if f.ParentObject.Name == "Query" && arg.Name == "id" {
			args = append(args, fmt.Sprintf("%s %s", arg.Name, formatOutputType(arg.TypeRef)))
		} else {
			args = append(args, fmt.Sprintf("%s %s", arg.Name, formatInputType(arg.TypeRef)))
		}
	}
	// Options (e.g. DirectoryContentsOptions -> <Object><Field>Options)
	if f.Args.HasOptionals() {
		args = append(
			args,
			fmt.Sprintf("opts ...%s", fieldOptionsStructName(f)),
		)
	}
	signature += "(" + strings.Join(args, ", ") + ")"

	retType := ""
	if f.TypeRef.IsScalar() || f.TypeRef.IsList() {
		retType = fmt.Sprintf("(%s, error)", formatOutputType(f.TypeRef))
	} else {
		retType = "*" + formatOutputType(f.TypeRef)
	}
	signature += " " + retType

	return signature
}
