package templates

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"text/template"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

func RubyTemplateFuncs(
	schemaVersion string,
) template.FuncMap {
	commonFunc := generator.NewCommonFunctions(schemaVersion, &FormatTypeFunc{})
	return template.FuncMap{
		"CommentToLines":      commentToLines,
		"FormatDeprecation":   formatDeprecation,
		"FormatInputType":     commonFunc.FormatInputType,
		"FormatOutputType":    commonFunc.FormatOutputType,
		"FormatName":          formatName,
		"FormatArg":           formatArg,
		"FormatMethod":        formatArg,
		"QueryToClient":       queryToClient,
		"GetOptionalArgs":     getOptionalArgs,
		"GetRequiredArgs":     getRequiredArgs,
		"HasPrefix":           strings.HasPrefix,
		"ArgsHaveDescription": argsHaveDescription,
		"Subtract":            subtract,
		"Solve":               solve,
		"IsSelfChainable":     commonFunc.IsSelfChainable,
		"ValidTypes":          validTypes,
		"IsCustomScalar":      isCustomScalar,
		"IsEnum":              isEnum,
		"SortEnumFields":      sortEnumFields,
		"FormatEnum":          formatEnum,
		"FormatEnumValue":     formatEnumValue,
		"PascalCase":          pascalCase,
		"SortInputFields":     sortInputFields,
		"CustomScalars":       customScalars,
		"Enums":               enums,
		"Nodes":               nodes,
		"Inputs":              inputs,
		"NodesWithOpts":       nodesWithOpts,
	}
}

func pascalCase(name string) string {
	return strcase.ToCamel(name)
}

func formatEnum(s string) string {
	s = strings.ToLower(s)
	return strcase.ToCamel(s)
}

func formatEnumValue(s string) string {
	return strings.ToUpper(strcase.ToSnake(s))
}

func isCustomScalar(t *introspection.Type) bool {
	switch introspection.Scalar(t.Name) {
	case introspection.ScalarString, introspection.ScalarInt, introspection.ScalarFloat, introspection.ScalarBoolean:
		return false
	default:
		return t.Kind == introspection.TypeKindScalar
	}
}

func isEnum(t *introspection.Type) bool {
	return t.Kind == introspection.TypeKindEnum &&
		!strings.HasPrefix(t.Name, "_")
}

func sortEnumFields(s []introspection.EnumValue) []introspection.EnumValue {
	sort.SliceStable(s, func(i, j int) bool {
		return s[i].Name < s[j].Name
	})

	// Deduplicate enum values that map to the same Ruby constant name.
	// The Dagger schema can have both "GZIP" and "Gzip" which both
	// map to "GZIP" in Ruby.
	seen := make(map[string]bool)
	var deduped []introspection.EnumValue
	for _, v := range s {
		formatted := formatEnumValue(v.Name)
		if !seen[formatted] {
			seen[formatted] = true
			deduped = append(deduped, v)
		}
	}
	return deduped
}

func validTypes(types introspection.Types) introspection.Types {
	var res introspection.Types
	for _, t := range types {
		if strings.HasPrefix(t.Name, "_") {
			continue
		}
		res = append(res, t)
	}
	slices.SortStableFunc(res, func(a, b *introspection.Type) int {
		return strings.Compare(a.Name, b.Name)
	})
	return res
}

func customScalars(types introspection.Types) introspection.Types {
	var res introspection.Types
	for _, t := range types {
		if isCustomScalar(t) {
			res = append(res, t)
		}
	}
	slices.SortStableFunc(res, func(a, b *introspection.Type) int {
		return strings.Compare(a.Name, b.Name)
	})
	return res
}

func enums(types introspection.Types) introspection.Types {
	var res introspection.Types
	for _, t := range types {
		if isEnum(t) {
			res = append(res, t)
		}
	}
	slices.SortStableFunc(res, func(a, b *introspection.Type) int {
		return strings.Compare(a.Name, b.Name)
	})
	return res
}

func nodes(types introspection.Types) introspection.Types {
	var res introspection.Types
	for _, t := range types {
		if strings.HasPrefix(t.Name, "_") {
			continue
		}
		if len(t.Fields) == 0 {
			continue
		}
		res = append(res, t)
	}
	slices.SortStableFunc(res, func(a, b *introspection.Type) int {
		return strings.Compare(a.Name, b.Name)
	})
	return res
}

func nodesWithOpts(types introspection.Types) introspection.Types {
	var res introspection.Types
	n := nodes(types)
	for _, t := range n {
		for _, f := range t.Fields {
			if len(getOptionalArgs(f.Args)) > 0 {
				res = append(res, &introspection.Type{
					Kind:        t.Kind,
					Name:        t.Name,
					Description: t.Description,
					Fields: []*introspection.Field{
						f,
					},
				})
			}
		}
	}
	slices.SortStableFunc(res, func(a, b *introspection.Type) int {
		return strings.Compare(a.Name+a.Fields[0].Name, b.Name+b.Fields[0].Name)
	})
	return res
}

func inputs(types introspection.Types) introspection.Types {
	var res introspection.Types
	for _, t := range types {
		if len(t.InputFields) > 0 {
			res = append(res, t)
		}
	}
	return res
}

func snakeCase(str string) string {
	return strcase.ToSnake(str)
}

func subtract(a, b int) int {
	return a - b
}

func solve(field introspection.Field) bool {
	if field.TypeRef == nil {
		return false
	}
	return field.TypeRef.IsScalar() || field.TypeRef.IsList()
}

func commentToLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return []string{}
	}

	split := strings.Split(s, "\n")
	for i, line := range split {
		if len(line) > 0 {
			split[i] = " " + line
		}
	}
	return split
}

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

func formatName(s string) string {
	if _, isKeyword := rubyKeywords[strings.ToLower(s)]; isKeyword {
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

var rubyKeywords = map[string]struct{}{
	"BEGIN":      {},
	"END":        {},
	"alias":      {},
	"and":        {},
	"begin":      {},
	"break":      {},
	"case":       {},
	"class":      {},
	"def":        {},
	"defined?":   {},
	"do":         {},
	"else":       {},
	"elsif":      {},
	"end":        {},
	"ensure":     {},
	"false":      {},
	"for":        {},
	"if":         {},
	"initialize": {},
	"module":     {},
	"next":       {},
	"nil":        {},
	"not":        {},
	"or":         {},
	"redo":       {},
	"rescue":     {},
	"retry":      {},
	"return":     {},
	"self":       {},
	"super":      {},
	"then":       {},
	"true":       {},
	"undef":      {},
	"unless":     {},
	"until":      {},
	"when":       {},
	"while":      {},
	"yield":      {},
}

func formatArg(s string) string {
	return formatName(
		snakeCase(
			s))
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
