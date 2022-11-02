package templates

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/dagger/dagger/codegen/introspection"
)

var (
	FuncMap = template.FuncMap{
		"CommentToLines":         CommentToLines,
		"FormatInputType":        FormatInputType,
		"FormatOutputType":       FormatOutputType,
		"FormatName":             FormatName,
		"FieldOptionsStructName": FieldOptionsStructName,
		"FieldFunction":          FieldFunction,
		"Subtract":               Subtract,
	}
)

// Subtract subtract integer a with integer b.
func Subtract(a, b int) int {
	return a - b
}

func CommentToLines(s string) []string {
	split := strings.Split(s, "\n")
	return split
}

// formatType formats a GraphQL type into Go
// Example: `String` -> `string`
func FormatInputType(r *introspection.TypeRef) string {
	return formatType(r, true)
}

func FormatOutputType(r *introspection.TypeRef) string {
	return formatType(r, false)
}

// formatType formats a GraphQL type into TypeScript
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
			case introspection.ScalarInt, introspection.ScalarFloat:
				representation += "number"
				return representation
			case introspection.ScalarBoolean:
				representation += "bool"
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

// FieldOptionsStructName returns the options struct name for a given field
func FieldOptionsStructName(f introspection.Field) string {
	// TODO: check this works correctly

	// Exception: `Query` option structs are not prefixed by `Query`.
	// This is just so that they're nicer to work with, e.g.
	// `ContainerOpts` rather than `QueryContainerOpts`
	// The structure name will not clash with others since everybody else
	// is prefixed by object name.
	if f.ParentObject.Name == "Query" {
		return FormatName(f.Name) + "Opts"
	}
	return FormatName(f.ParentObject.Name) + FormatName(f.Name) + "Opts"
}

// FieldFunction converts a field into a function signature
// Example: `contents: String!` -> `contents() string`
// TODO transform into template as well?
func FieldFunction(f introspection.Field) string {
	solve := f.TypeRef.IsScalar() || f.TypeRef.IsList()
	var async string
	if solve {
		async = "async"
	}
	//TODO think about the await in the func body

	signature := fmt.Sprintf(`%s %s`,
		async,
		FormatName(f.Name),
	)

	// Generate arguments
	args := []string{}
	for _, arg := range f.Args {
		if !arg.TypeRef.IsOptional() {
			args = append(args, fmt.Sprintf("%s %s", FormatInputType(arg.TypeRef), arg.Name))
		}
	}

	// Options (e.g. DirectoryContentsOptions -> <Object><Field>Options)
	if f.Args.HasOptionals() {
		// TODO iterate through optional args?
		args = append(
			args,
			fmt.Sprintf("args: { %s }", FieldOptionsStructName(f)),
		)
	}
	signature += "(" + strings.Join(args, ", ") + ")"

	_ = async
	retType := FormatOutputType(f.TypeRef)
	signature += ": " + retType

	// FIXME: just use fmt.Sprintf?
	//signature = fmt.Sprintf("%s %s(%s): %s", async, funcName, argString, retType)

	return signature
}
