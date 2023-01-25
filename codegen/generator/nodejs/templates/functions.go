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
		"FormatEnum":          commonFunc.FormatEnum,
		"FormatName":          formatName,
		"GetOptionalArgs":     getOptionalArgs,
		"GetRequiredArgs":     getRequiredArgs,
		"HasPrefix":           strings.HasPrefix,
		"PascalCase":          pascalCase,
		"IsArgOptional":       isArgOptional,
		"IsCustomScalar":      isCustomScalar,
		"IsEnum":              isEnum,
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

// isEnum checks if the type is actually custom.
func isEnum(t *introspection.Type) bool {
	return t.Kind == introspection.TypeKindEnum &&
		// We ignore the internal GraphQL enums
		!strings.HasPrefix(t.Name, "__")
}

// formatType formats a GraphQL type into TypeScript
// Example: `String` -> `string`
func formatType(r *introspection.TypeRef, input bool) (representation string) {
	var isList bool
	for ref := r; ref != nil; ref = ref.OfType {
		switch ref.Kind {
		case introspection.TypeKindList:
			isList = true
			// add [] as suffix to the type
			defer func() {
				// dolanor: hackish way to handle this. Otherwise needs to refactor the whole loop logic.
				if isList {
					representation += "[]"
				}
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

				// When used as an input, we're going to add objects as an alternative to ID scalars (e.g. `Container` and `ContainerID`)
				// FIXME: do this dynamically rather than a hardcoded map.
				rewrite := map[string]string{
					"ContainerID": "Container",
					"FileID":      "File",
					"DirectoryID": "Directory",
					"SecretID":    "Secret",
					"SocketID":    "Socket",
					"CacheID":     "CacheVolume",
				}
				if alias, ok := rewrite[ref.Name]; ok && input {
					listChars := "[]"
					if isList {
						representation += alias + listChars
					} else {
						representation += alias
					}
					isList = false
				} else {
					representation += ref.Name
				}
				return representation
			}
		case introspection.TypeKindObject:
			name := ref.Name
			if name == "Query" {
				name = "Client"
			}
			representation += formatName(name)
			return representation
		case introspection.TypeKindInputObject:
			representation += formatName(ref.Name)
			return representation
		case introspection.TypeKindEnum:
			representation += formatEnum(ref.Name)
			return representation
		}
	}

	panic(r)
}

// formatName formats a GraphQL name (e.g. object, field, arg) into a TS equivalent
func formatName(s string) string {
	return s
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
