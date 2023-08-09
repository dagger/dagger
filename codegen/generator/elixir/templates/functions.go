package templates

import (
	"bytes"
	"fmt"
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
		"DocComment":        docComment,
		"FormatDeprecation": formatDeprecation,
		// "FormatReturnType":        commonFunc.FormatReturnType,
		"FormatInputType": commonFunc.FormatInputType,
		// "FormatOutputType":        commonFunc.FormatOutputType,
		"FormatName":     formatName,
		"FormatModule":   formatModule,
		"FormatFunction": formatFunction,
		"FormatEnum":     formatEnum,
		"FormatEnumType": formatEnumType,
		"SortEnumFields": sortEnumFields,
		// "FieldOptionsStructName":  fieldOptionsStructName,
		// "FieldFunction":           fieldFunction,
		"IsEnum": isEnum,
		// "GetArrayField":           commonFunc.GetArrayField,
		// "IsListOfObject":          commonFunc.IsListOfObject,
		// "ToLowerCase":             commonFunc.ToLowerCase,
		// "ToUpperCase":             commonFunc.ToUpperCase,
		// "FormatArrayField":        formatArrayField,
		// "FormatArrayToSingleType": formatArrayToSingleType,
		// "ConvertID":               commonFunc.ConvertID,
		// "IsSelfChainable":         commonFunc.IsSelfChainable,
	}
)

// comments out a string
// Example: `hello\nworld` -> `# hello\n# world\n`
func docComment(s string) string {
	if s == "" {
		return ""
	}

	var out bytes.Buffer
	out.WriteString(`"""`)
	out.WriteByte('\n')
	out.WriteString(s)
	out.WriteByte('\n')
	out.WriteString(`"""`)

	return out.String()
}

func formatDeprecation(s string) string {
	return `"` + s + `"`
}

func formatName(s string) string {
	if s == generator.QueryStructName {
		return generator.QueryStructClientName
	}
	return strcase.ToCamel(s)
}

func formatModule(s string) string {
	return "Dagger." + formatName(s)
}

func formatFunction(s string) string {
	return strcase.ToSnake(s)
}

// formatName formats a GraphQL Enum value into a Go equivalent
// Example: `fooId` -> `FooID`
func formatEnum(s string) string {
	return ":" + s
}

func formatEnumType(s []introspection.EnumValue) string {
	names := make([]string, len(s))
	for i, ev := range s {
		names[i] = formatEnum(ev.Name)
	}
	return strings.Join(names, " | ")
}

func sortEnumFields(s []introspection.EnumValue) []introspection.EnumValue {
	sort.SliceStable(s, func(i, j int) bool {
		return s[i].Name < s[j].Name
	})
	return s
}

func isEnum(t introspection.Type) bool {
	return t.Kind == introspection.TypeKindEnum && !strings.HasPrefix(t.Name, "__")
}

type FormatTypeFunc struct{}

func (f *FormatTypeFunc) FormatKindList(representation string) string {
	return fmt.Sprintf("[%s.t()]", representation)
}

func (f *FormatTypeFunc) FormatKindScalarString(representation string) string {
	return "String.t()"
}

func (f *FormatTypeFunc) FormatKindScalarInt(representation string) string {
	return "integer()"
}

func (f *FormatTypeFunc) FormatKindScalarFloat(representation string) string {
	return "float()"
}

func (f *FormatTypeFunc) FormatKindScalarBoolean(representation string) string {
	return "boolean()"
}

func (f *FormatTypeFunc) FormatKindScalarDefault(representation string, refName string, input bool) string {
	if alias, ok := generator.CustomScalar[refName]; ok {
		return fmt.Sprintf("%s.t()", alias)
	}
	return fmt.Sprintf("%s.t()", refName)
}

func (f *FormatTypeFunc) FormatKindObject(representation string, refName string) string {
	return fmt.Sprintf("%s.t()", formatModule(refName))
}

func (f *FormatTypeFunc) FormatKindInputObject(representation string, refName string) string {
	return fmt.Sprintf("%s.t()", formatModule(refName))
}

func (f *FormatTypeFunc) FormatKindEnum(representation string, refName string) string {
	return fmt.Sprintf("%s.t()", formatModule(refName))
}
