package templates

import (
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/codegen/introspection"
)

var (
	funcMap = template.FuncMap{
		"CommentToLines":    commentToLines,
		"FormatDeprecation": formatDeprecation,
		"FormatInputType":   formatInputType,
		"FormatOutputType":  formatOutputType,
		"FormatName":        formatName,
		"GetOptionalArgs":   getOptionalArgs,
		"GetRequiredArgs":   getRequiredArgs,
		"HasPrefix":         strings.HasPrefix,
		"PascalCase":        pascalCase,
		"IsArgOptional":     isArgOptional,
		"IsCustomScalar":    isCustomScalar,
		"SortInputFields":   sortInputFields,
		"Solve":             solve,
		"Subtract":          subtract,
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

// formatType formats a GraphQL type into Go
// Example: `String` -> `string`
// TODO: maybe delete and only use formatType?
func formatInputType(r *introspection.TypeRef) string {
	return formatType(r, true)
}

// TODO: maybe delete and only use formatType?
func formatOutputType(r *introspection.TypeRef) string {
	return formatType(r, false)
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
					"CacheID":     "CacheVolume",
				}
				if alias, ok := rewrite[ref.Name]; ok && input {
					listChars := "[]"
					if isList {
						representation += ref.Name + listChars + " | " + alias + listChars
					} else {
						representation += ref.Name + " | " + alias
					}
					isList = false
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
