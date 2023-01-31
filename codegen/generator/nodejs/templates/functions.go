package templates

import (
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
		"CommentToLines":      commentToLines,
		"FormatDeprecation":   formatDeprecation,
		"FormatInputType":     commonFunc.FormatInputType,
		"FormatOutputType":    commonFunc.FormatOutputType,
		"FormatName":          formatName,
		"GetOptionalArgs":     getOptionalArgs,
		"GetRequiredArgs":     getRequiredArgs,
		"HasPrefix":           strings.HasPrefix,
		"PascalCase":          pascalCase,
		"IsArgOptional":       isArgOptional,
		"IsCustomScalar":      isCustomScalar,
		"ArgsHaveDescription": argsHaveDescription,
		"SortInputFields":     sortInputFields,
		"Solve":               solve,
		"Subtract":            subtract,
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
	s = strings.TrimSpace(s)
	if s == "" {
		return []string{}
	}

	split := strings.Split(s, "\n")
	return split
}

// format the deprecation reason
// Example: `Replaced by @foo.` -> `// Replaced by Foo\n`
func formatDeprecation(s string) []string {
	r := regexp.MustCompile("`[a-zA-Z0-9_]+`")
	matches := r.FindAllString(s, -1)
	for _, match := range matches {
		replacement := strings.TrimPrefix(match, "`")
		replacement = strings.TrimSuffix(replacement, "`")
		replacement = formatName(replacement)
		s = strings.ReplaceAll(s, match, replacement)
	}
	return commentToLines("@deprecated " + s)
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

func splitRequiredOptionalArgs(values introspection.InputValues) (required introspection.InputValues, optionals introspection.InputValues) {
	for i, v := range values {
		if v.TypeRef != nil && !v.TypeRef.IsOptional() {
			continue
		}
		return values[:i], values[i:]
	}
	return values, nil
}

func getRequiredArgs(values introspection.InputValues) introspection.InputValues {
	required, _ := splitRequiredOptionalArgs(values)
	return required
}

func getOptionalArgs(values introspection.InputValues) introspection.InputValues {
	_, optional := splitRequiredOptionalArgs(values)
	return optional
}

func sortInputFields(s []introspection.InputValue) []introspection.InputValue {
	sort.SliceStable(s, func(i, j int) bool {
		return s[i].Name < s[j].Name
	})
	return s
}

func argsHaveDescription(values introspection.InputValues) bool {
	for _, o := range values {
		if strings.TrimSpace(o.Description) != "" {
			return true
		}
	}

	return false
}
