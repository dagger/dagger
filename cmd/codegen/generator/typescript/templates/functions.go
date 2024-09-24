package templates

import (
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

func TypescriptTemplateFuncs(
	schemaVersion string,
) template.FuncMap {
	commonFunc := generator.NewCommonFunctions(schemaVersion, &FormatTypeFunc{})
	return template.FuncMap{
		"CommentToLines":            commentToLines,
		"FormatDeprecation":         formatDeprecation,
		"FormatReturnType":          commonFunc.FormatReturnType,
		"FormatInputType":           commonFunc.FormatInputType,
		"FormatOutputType":          commonFunc.FormatOutputType,
		"FormatEnum":                formatEnum,
		"FormatName":                formatName,
		"QueryToClient":             queryToClient,
		"GetOptionalArgs":           getOptionalArgs,
		"GetRequiredArgs":           getRequiredArgs,
		"HasPrefix":                 strings.HasPrefix,
		"PascalCase":                pascalCase,
		"IsArgOptional":             isArgOptional,
		"IsCustomScalar":            isCustomScalar,
		"IsEnum":                    isEnum,
		"IsKeyword":                 isKeyword,
		"ArgsHaveDescription":       argsHaveDescription,
		"SortInputFields":           sortInputFields,
		"SortEnumFields":            sortEnumFields,
		"Solve":                     solve,
		"Subtract":                  subtract,
		"ConvertID":                 commonFunc.ConvertID,
		"IsSelfChainable":           commonFunc.IsSelfChainable,
		"IsListOfObject":            commonFunc.IsListOfObject,
		"GetArrayField":             commonFunc.GetArrayField,
		"ToLowerCase":               commonFunc.ToLowerCase,
		"ToUpperCase":               commonFunc.ToUpperCase,
		"ToSingleType":              toSingleType,
		"GetEnumValues":             getEnumValues,
		"CheckVersionCompatibility": commonFunc.CheckVersionCompatibility,
	}
}

// pascalCase change a type name into pascalCase
func pascalCase(name string) string {
	return strcase.ToCamel(name)
}

// solve checks if a field is solvable.
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
		return t.Kind == introspection.TypeKindScalar
	}
}

// isEnum checks if the type is actually custom.
func isEnum(t *introspection.Type) bool {
	return t.Kind == introspection.TypeKindEnum &&
		// We ignore the internal GraphQL enums
		!strings.HasPrefix(t.Name, "_")
}

func isKeyword(s string) bool {
	_, isKeyword := jsKeywords[strings.ToLower(s)]

	return isKeyword
}

// formatName formats a GraphQL name (e.g. object, field, arg) into a TS
// equivalent, avoiding collisions with reserved words.
func formatName(s string) string {
	if _, isKeyword := jsKeywords[strings.ToLower(s)]; isKeyword {
		// NB: this is case-insensitive; in JS, both function and Function cause
		// problems (one straight up doesn't parse, the other causes lint errors)
		return s + "_"
	}
	return s
}

func queryToClient(s string) string {
	if s == generator.QueryStructName {
		return generator.QueryStructClientName
	}
	return s
}

// all words to avoid collisions with, whether they're reserved or not
//
// in practice, many of these work just fine as e.g. method
// names, like 'export' and 'from'.
var jsKeywords = map[string]struct{}{
	"await":    {},
	"break":    {},
	"case":     {},
	"catch":    {},
	"class":    {},
	"const":    {},
	"continue": {},
	"debugger": {},
	"default":  {},
	"delete":   {},
	"do":       {},
	"else":     {},
	"enum":     {},
	// "export":     {}, // containr.export
	"extends":    {},
	"false":      {},
	"finally":    {},
	"for":        {},
	"function":   {},
	"if":         {},
	"implements": {},
	"import":     {},
	"in":         {},
	"instanceof": {},
	"interface":  {},
	"new":        {},
	"null":       {},
	"package":    {},
	"private":    {},
	"protected":  {},
	"public":     {},
	"return":     {},
	"super":      {},
	"switch":     {},
	"this":       {},
	"throw":      {},
	"true":       {},
	"try":        {},
	"typeof":     {},
	"var":        {},
	"void":       {},
	"while":      {},
	// "with":        {},
	"yield":       {},
	"as":          {},
	"let":         {},
	"static":      {},
	"any":         {},
	"boolean":     {},
	"constructor": {},
	"declare":     {},
	// "get":         {},
	"module":  {},
	"require": {},
	"number":  {},
	"set":     {},
	"string":  {},
	"symbol":  {},
	"type":    {},
	// "from":        {}, // container.from
	// "of":        {},
	"async":     {},
	"namespace": {},
}

// formatEnum formats a GraphQL enum into a TS equivalent
func formatEnum(s string) string {
	s = strings.ToLower(s)
	return strcase.ToCamel(s)
}

// isArgOptional checks if some arg are optional.
// They are, if all of there InputValues are optional.
func isArgOptional(values introspection.InputValues) bool {
	for _, v := range values {
		if !v.IsOptional() {
			return false
		}
	}
	return true
}

func splitRequiredOptionalArgs(values introspection.InputValues) (required introspection.InputValues, optionals introspection.InputValues) {
	for i, v := range values {
		if !v.IsOptional() {
			continue
		}

		return values[:i], values[i:]
	}
	return values, nil
}

func getEnumValues(values introspection.InputValues) introspection.InputValues {
	enums := introspection.InputValues{}

	for _, v := range values {
		if v.TypeRef != nil && v.TypeRef.Kind == introspection.TypeKindEnum {
			enums = append(enums, v)
		}

		// Check parent if the parent is an enum (for instance with TypeDefKind)
		if v.TypeRef.OfType != nil && v.TypeRef.OfType.Kind == introspection.TypeKindEnum {
			enums = append(enums, v)
		}
	}

	return enums
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

func sortEnumFields(s []introspection.EnumValue) []introspection.EnumValue {
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

func toSingleType(value string) string {
	return value[:len(value)-2]
}
