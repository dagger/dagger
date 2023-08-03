package templates

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/codegen/generator"
	"github.com/dagger/dagger/codegen/introspection"
)

var (
	commonFunc = generator.NewCommonFunctions(&FormatTypeFunc{})
	funcMap    = template.FuncMap{
		"Comment":                 comment,
		"FormatDeprecation":       formatDeprecation,
		"FormatReturnType":        commonFunc.FormatReturnType,
		"FormatInputType":         commonFunc.FormatInputType,
		"FormatOutputType":        commonFunc.FormatOutputType,
		"FormatName":              formatName,
		"FormatEnum":              formatEnum,
		"SortEnumFields":          sortEnumFields,
		"FieldOptionsStructName":  fieldOptionsStructName,
		"FieldFunction":           fieldFunction,
		"IsEnum":                  isEnum,
		"GetArrayField":           commonFunc.GetArrayField,
		"IsListOfObject":          commonFunc.IsListOfObject,
		"ToLowerCase":             commonFunc.ToLowerCase,
		"ToUpperCase":             commonFunc.ToUpperCase,
		"FormatArrayField":        formatArrayField,
		"FormatArrayToSingleType": formatArrayToSingleType,
		"ConvertID":               commonFunc.ConvertID,
		"IsSelfChainable":         commonFunc.IsSelfChainable,
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

// format the deprecation reason
// Example: `Replaced by @foo.` -> `// Replaced by Foo\n`
func formatDeprecation(s string) string {
	r := regexp.MustCompile("`[a-zA-Z0-9_]+`")
	matches := r.FindAllString(s, -1)
	for _, match := range matches {
		replacement := strings.TrimPrefix(match, "`")
		replacement = strings.TrimSuffix(replacement, "`")
		replacement = formatName(replacement)
		s = strings.ReplaceAll(s, match, replacement)
	}
	return comment("Deprecated: " + s)
}

func isEnum(t introspection.Type) bool {
	return t.Kind == introspection.TypeKindEnum &&
		// We ignore the internal GraphQL enums
		!strings.HasPrefix(t.Name, "__")
}

// formatName formats a GraphQL name (e.g. object, field, arg) into a Go equivalent
// Example: `fooId` -> `FooID`
func formatName(s string) string {
	if s == generator.QueryStructName {
		return generator.QueryStructClientName
	}
	if len(s) > 0 {
		s = strings.ToUpper(string(s[0])) + s[1:]
	}
	return lintName(s)
}

// formatName formats a GraphQL Enum value into a Go equivalent
// Example: `fooId` -> `FooID`
func formatEnum(s string) string {
	s = strings.ToLower(s)
	return strcase.ToCamel(s)
}

func sortEnumFields(s []introspection.EnumValue) []introspection.EnumValue {
	sort.SliceStable(s, func(i, j int) bool {
		return s[i].Name < s[j].Name
	})
	return s
}

func formatArrayField(fields []*introspection.Field) string {
	result := []string{}

	for _, f := range fields {
		result = append(result, fmt.Sprintf("%s: &fields[i].%s", f.Name, commonFunc.ToUpperCase(f.Name)))
	}

	return strings.Join(result, ", ")
}

func formatArrayToSingleType(arrType string) string {
	return arrType[2:]
}

// fieldOptionsStructName returns the options struct name for a given field
func fieldOptionsStructName(f introspection.Field) string {
	// Exception: `Query` option structs are not prefixed by `Query`.
	// This is just so that they're nicer to work with, e.g.
	// `ContainerOpts` rather than `QueryContainerOpts`
	// The structure name will not clash with others since everybody else
	// is prefixed by object name.
	if f.ParentObject.Name == generator.QueryStructName {
		return formatName(f.Name) + "Opts"
	}
	return formatName(f.ParentObject.Name) + formatName(f.Name) + "Opts"
}

// fieldFunction converts a field into a function signature
// Example: `contents: String!` -> `func (r *File) Contents(ctx context.Context) (string, error)`
func fieldFunction(f introspection.Field) string {
	structName := formatName(f.ParentObject.Name)
	signature := fmt.Sprintf(`func (r *%s) %s`,
		structName, formatName(f.Name))

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
		if f.ParentObject.Name == generator.QueryStructName && arg.Name == "id" {
			args = append(args, fmt.Sprintf("%s %s", arg.Name, commonFunc.FormatOutputType(arg.TypeRef)))
		} else {
			args = append(args, fmt.Sprintf("%s %s", arg.Name, commonFunc.FormatInputType(arg.TypeRef)))
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

	retType := commonFunc.FormatReturnType(f)
	if f.TypeRef.IsScalar() || f.TypeRef.IsList() {
		retType = fmt.Sprintf("(%s, error)", retType)
	} else {
		retType = "*" + retType
	}
	signature += " " + retType

	return signature
}
