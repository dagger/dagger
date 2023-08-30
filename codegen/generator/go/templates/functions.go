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

func GoTemplateFuncs(envName string) template.FuncMap {
	return goTemplateFuncs{
		CommonFunctions: generator.NewCommonFunctions(&FormatTypeFunc{}),
		envName:         envName,
	}.FuncMap()
}

type goTemplateFuncs struct {
	*generator.CommonFunctions
	envName string
}

func (funcs goTemplateFuncs) FuncMap() template.FuncMap {
	return template.FuncMap{
		// common
		"FormatReturnType": funcs.FormatReturnType,
		"FormatInputType":  funcs.FormatInputType,
		"FormatOutputType": funcs.FormatOutputType,
		"GetArrayField":    funcs.GetArrayField,
		"IsListOfObject":   funcs.IsListOfObject,
		"ToLowerCase":      funcs.ToLowerCase,
		"ToUpperCase":      funcs.ToUpperCase,
		"ConvertID":        funcs.ConvertID,
		"IsSelfChainable":  funcs.IsSelfChainable,

		// go specific
		"Comment":                       funcs.comment,
		"FormatDeprecation":             funcs.formatDeprecation,
		"FormatName":                    formatName,
		"FormatEnum":                    funcs.formatEnum,
		"SortEnumFields":                funcs.sortEnumFields,
		"FieldOptionsStructName":        funcs.fieldOptionsStructName,
		"FieldFunction":                 funcs.fieldFunction,
		"IsEnum":                        funcs.isEnum,
		"FormatArrayField":              funcs.formatArrayField,
		"FormatArrayToSingleType":       funcs.formatArrayToSingleType,
		"EnvironmentWithMethodPreamble": funcs.environmentWithMethodPreamble,
	}
}

// comments out a string
// Example: `hello\nworld` -> `// hello\n// world\n`
func (funcs goTemplateFuncs) comment(s string) string {
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
func (funcs goTemplateFuncs) formatDeprecation(s string) string {
	r := regexp.MustCompile("`[a-zA-Z0-9_]+`")
	matches := r.FindAllString(s, -1)
	for _, match := range matches {
		replacement := strings.TrimPrefix(match, "`")
		replacement = strings.TrimSuffix(replacement, "`")
		replacement = formatName(replacement)
		s = strings.ReplaceAll(s, match, replacement)
	}
	return funcs.comment("Deprecated: " + s)
}

func (funcs goTemplateFuncs) isEnum(t introspection.Type) bool {
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

// formatEnum formats a GraphQL Enum value into a Go equivalent
// Example: `fooId` -> `FooID`
func (funcs goTemplateFuncs) formatEnum(s string) string {
	s = strings.ToLower(s)
	return strcase.ToCamel(s)
}

func (funcs goTemplateFuncs) sortEnumFields(s []introspection.EnumValue) []introspection.EnumValue {
	sort.SliceStable(s, func(i, j int) bool {
		return s[i].Name < s[j].Name
	})
	return s
}

func (funcs goTemplateFuncs) formatArrayField(fields []*introspection.Field) string {
	result := []string{}

	for _, f := range fields {
		result = append(result, fmt.Sprintf("%s: &fields[i].%s", f.Name, funcs.ToUpperCase(f.Name)))
	}

	return strings.Join(result, ", ")
}

func (funcs goTemplateFuncs) formatArrayToSingleType(arrType string) string {
	return arrType[2:]
}

// fieldOptionsStructName returns the options struct name for a given field
func (funcs goTemplateFuncs) fieldOptionsStructName(f introspection.Field) string {
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
func (funcs goTemplateFuncs) fieldFunction(f introspection.Field) string {
	// don't create methods on query for the env itself,
	// e.g. don't create `func (r *DAG) Go() *Go` in the Go env's codegen
	if envName := funcs.envName; envName != "" {
		if f.ParentObject.Name == generator.QueryStructName && f.Name == envName {
			return ""
		}
	}

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
			args = append(args, fmt.Sprintf("%s %s", arg.Name, funcs.FormatOutputType(arg.TypeRef)))
		} else if funcs.envName != "" && formatName(f.ParentObject.Name) == "Environment" && arg.Name == "id" {
			args = append(args, fmt.Sprintf("%s any", arg.Name))
		} else {
			args = append(args, fmt.Sprintf("%s %s", arg.Name, funcs.FormatInputType(arg.TypeRef)))
		}
	}

	// Options (e.g. DirectoryContentsOptions -> <Object><Field>Options)
	if f.Args.HasOptionals() {
		args = append(
			args,
			fmt.Sprintf("opts ...%s", funcs.fieldOptionsStructName(f)),
		)
	}
	signature += "(" + strings.Join(args, ", ") + ")"

	retType := funcs.FormatReturnType(f)
	if f.TypeRef.IsScalar() || f.TypeRef.IsList() {
		retType = fmt.Sprintf("(%s, error)", retType)
	} else {
		retType = "*" + retType
	}
	signature += " " + retType

	return signature
}

func (funcs goTemplateFuncs) environmentWithMethodPreamble(f introspection.Field) string {
	if funcs.envName == "" || f.ParentObject.Name != "Environment" || !strings.HasPrefix(f.Name, "with") {
		return ""
	}
	if f.Name == "withWorkdir" {
		return ""
	}
	return fmt.Sprintf(`res := %s(r, id)
if res != nil {
	return res
}
`, formatName(f.Name))
}
