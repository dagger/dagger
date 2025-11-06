package templates

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"go/token"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"github.com/iancoleman/strcase"
	"golang.org/x/tools/go/packages"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

func GoTemplateFuncs(
	ctx context.Context,
	schema *introspection.Schema,
	schemaVersion string,
	cfg generator.Config,
	pkg *packages.Package,
	fset *token.FileSet,
	pass int,
) template.FuncMap {
	return goTemplateFuncs{
		CommonFunctions: generator.NewCommonFunctions(schemaVersion, &FormatTypeFunc{}),
		ctx:             ctx,
		cfg:             cfg,
		modulePkg:       pkg,
		moduleFset:      fset,
		schema:          schema,
		schemaVersion:   schemaVersion,
		pass:            pass,
	}.FuncMap()
}

type goTemplateFuncs struct {
	*generator.CommonFunctions
	ctx           context.Context
	cfg           generator.Config
	modulePkg     *packages.Package
	moduleFset    *token.FileSet
	schema        *introspection.Schema
	schemaVersion string
	pass          int
}

func (funcs goTemplateFuncs) FuncMap() template.FuncMap {
	return template.FuncMap{
		// common
		"FormatReturnType":          funcs.FormatReturnType,
		"FormatInputType":           funcs.FormatInputType,
		"FormatOutputType":          funcs.FormatOutputType,
		"GetArrayField":             funcs.GetArrayField,
		"IsListOfObject":            funcs.IsListOfObject,
		"ToLowerCase":               funcs.ToLowerCase,
		"ToUpperCase":               funcs.ToUpperCase,
		"ConvertID":                 funcs.ConvertID,
		"IsSelfChainable":           funcs.IsSelfChainable,
		"IsIDableObject":            funcs.IsIDableObject,
		"InnerType":                 funcs.InnerType,
		"ObjectName":                funcs.ObjectName,
		"CheckVersionCompatibility": funcs.CheckVersionCompatibility,

		// go specific
		"Comment":                 funcs.comment,
		"FormatDeprecation":       funcs.formatDeprecation,
		"FormatExperimental":      funcs.formatExperimental,
		"FormatName":              formatName,
		"FormatEnum":              funcs.formatEnum,
		"SortEnumFields":          funcs.sortEnumFields,
		"GroupEnumByValue":        funcs.groupEnumByValue,
		"FieldOptionsStructName":  funcs.fieldOptionsStructName,
		"FieldFunction":           funcs.fieldFunction,
		"IsArgOptional":           funcs.isArgOptional,
		"HasOptionals":            funcs.hasOptionals,
		"IsEnum":                  funcs.isEnum,
		"IsPointer":               funcs.isPointer,
		"FormatArrayField":        funcs.formatArrayField,
		"FormatArrayToSingleType": funcs.formatArrayToSingleType,
		"IsPartial":               funcs.isPartial,
		"IsModuleCode":            funcs.isModuleCode,
		"IsStandaloneClient":      funcs.isStandaloneClient,
		"ModuleMainSrc":           funcs.moduleMainSrc,
		"ModuleRelPath":           funcs.moduleRelPath,
		"Dependencies":            funcs.Dependencies,
		"HasLocalDependencies":    funcs.HasLocalDependencies,
		"json":                    funcs.json,
	}
}

func (goTemplateFuncs) json(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
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
	return funcs.formatHelper("Deprecated", s)
}

func (funcs goTemplateFuncs) formatExperimental(s string) string {
	return funcs.formatHelper("Experimental", s)
}

func (funcs goTemplateFuncs) formatHelper(name string, s string) string {
	r := regexp.MustCompile("`[a-zA-Z0-9_]+`")
	matches := r.FindAllString(s, -1)
	for _, match := range matches {
		replacement := strings.TrimPrefix(match, "`")
		replacement = strings.TrimSuffix(replacement, "`")
		replacement = formatName(replacement)
		s = strings.ReplaceAll(s, match, replacement)
	}
	return funcs.comment(name + ": " + s)
}

func (funcs goTemplateFuncs) isEnum(t introspection.Type) bool {
	return t.Kind == introspection.TypeKindEnum &&
		// We ignore the internal GraphQL enums
		!strings.HasPrefix(t.Name, "_")
}

// isPointer returns true if value is a pointer.
func (funcs goTemplateFuncs) isPointer(t introspection.InputValue) (bool, error) {
	// Ignore id since it's converted to special ID type later.
	if t.Name == "id" {
		return false, nil
	}

	// Convert to a string representation to avoid code repetition.
	representation, err := funcs.FormatInputType(t.TypeRef)
	return strings.Index(representation, "*") == 0, err
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
// Example: `FOO_VALUE` -> `FooValue`, `FooValue` -> `FooValue`
func (funcs goTemplateFuncs) formatEnum(parent string, s string) string {
	if parent == "" {
		// legacy path - terrible, removes all the casing :(
		s = strings.ToLower(s)
	}
	s = strcase.ToCamel(s)
	return parent + s
}

func (funcs goTemplateFuncs) sortEnumFields(s []introspection.EnumValue) []introspection.EnumValue {
	s = slices.Clone(s)
	slices.SortStableFunc(s, func(x, y introspection.EnumValue) int {
		return cmp.Compare(strcase.ToCamel(x.Name), strcase.ToCamel(y.Name))
	})
	s = slices.CompactFunc(s, func(x, y introspection.EnumValue) bool {
		return strcase.ToCamel(x.Name) == strcase.ToCamel(y.Name)
	})
	return s
}

// groupEnumByValue returns a list of lists of enums, grouped by similar enum value.
//
// Additionally, enum names within a single value are removed (which would
// result in duplicate codegen).
func (funcs goTemplateFuncs) groupEnumByValue(s []introspection.EnumValue) [][]introspection.EnumValue {
	m := map[string][]introspection.EnumValue{}
	for _, v := range s {
		value := cmp.Or(v.Directives.EnumValue(), v.Name)
		if !slices.ContainsFunc(m[value], func(other introspection.EnumValue) bool {
			return strcase.ToCamel(v.Name) == strcase.ToCamel(other.Name)
		}) {
			m[value] = append(m[value], v)
		}
	}

	var result [][]introspection.EnumValue
	for _, v := range s {
		value := cmp.Or(v.Directives.EnumValue(), v.Name)
		if res, ok := m[value]; ok {
			result = append(result, res)
			delete(m, value)
		}
	}
	return result
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
func (funcs goTemplateFuncs) fieldOptionsStructName(f introspection.Field, scopes ...string) string {
	// Exception: `Query` option structs are not prefixed by `Query`.
	// This is just so that they're nicer to work with, e.g.
	// `ContainerOpts` rather than `QueryContainerOpts`
	// The structure name will not clash with others since everybody else
	// is prefixed by object name.
	scope := strings.Join(scopes, "")
	if scope != "" {
		scope += "."
	}
	if f.ParentObject.Name == generator.QueryStructName {
		return scope + formatName(f.Name) + "Opts"
	}
	return scope + formatName(f.ParentObject.Name) + formatName(f.Name) + "Opts"
}

// hasOptionals returns true if a field has optional arguments
//
// Note: This is only necessary to simplify backwards compatibility of a breaking change.
func (funcs goTemplateFuncs) hasOptionals(i introspection.InputValues) bool {
	if funcs.CheckVersionCompatibility("v0.13.0") {
		return i.HasOptionals()
	}
	return slices.ContainsFunc(i, funcs.isArgOptional)
}

// isArgOptional returns true if a field argument is optional
//
// Note: This is only necessary to simplify backwards compatibility of a breaking change.
func (funcs goTemplateFuncs) isArgOptional(arg introspection.InputValue) bool {
	if funcs.CheckVersionCompatibility("v0.13.0") {
		return arg.IsOptional()
	}
	return arg.TypeRef.IsOptional()
}

// fieldFunction converts a field into a function signature
// Example: `contents: String!` -> `func (r *File) Contents(ctx context.Context) (string, error)`
func (funcs goTemplateFuncs) fieldFunction(f introspection.Field, topLevel bool, supportsVoid bool, scopes ...string) (string, error) {
	// don't create methods on query for the env itself,
	// e.g. don't create `func (r *DAG) Go() *Go` in the Go env's codegen
	// TODO(vito): still needed? we codegen against the module's schema view,
	// which shouldn't include the module itself, only its dependencies. or is
	// this because of universe?
	// if moduleName := funcs.moduleName; moduleName != "" {
	// 	if f.ParentObject.Name == generator.QueryStructName && f.Name == moduleName {
	// 		return ""
	// 	}
	// }

	structName := formatName(f.ParentObject.Name)
	signature := "func "
	if !topLevel {
		signature += `(r *` + structName + `) `
	}
	signature += formatName(f.Name)

	// Generate arguments
	args := []string{}
	if f.TypeRef.IsScalar() || f.TypeRef.IsList() {
		args = append(args, "ctx context.Context")
	}
	for _, arg := range f.Args {
		if funcs.isArgOptional(arg) {
			continue
		}

		// FIXME: For top-level queries (e.g. File, Directory) if the field is named `id` then keep it as a
		// scalar (DirectoryID) rather than an object (*Directory).
		if f.ParentObject.Name == generator.QueryStructName && arg.Name == "id" {
			outType, err := funcs.FormatOutputType(arg.TypeRef, scopes...)
			if err != nil {
				return "", err
			}
			args = append(args, fmt.Sprintf("%s %s", arg.Name, outType))
		} else {
			inType, err := funcs.FormatInputType(arg.TypeRef, scopes...)
			if err != nil {
				return "", err
			}
			args = append(args, fmt.Sprintf("%s %s", arg.Name, inType))
		}
	}

	// Options (e.g. DirectoryContentsOptions -> <Object><Field>Options)
	if funcs.hasOptionals(f.Args) {
		args = append(
			args,
			fmt.Sprintf("opts ...%s", funcs.fieldOptionsStructName(f, scopes...)),
		)
	}
	signature += "(" + strings.Join(args, ", ") + ")"

	retType, err := funcs.FormatReturnType(f, scopes...)
	if err != nil {
		return "", err
	}
	switch {
	case supportsVoid && f.TypeRef.IsVoid():
		retType = "error"
	case f.TypeRef.IsScalar() || f.TypeRef.IsList():
		retType = fmt.Sprintf("(%s, error)", retType)
	default:
		retType = "*" + retType
	}
	signature += " " + retType

	return signature, nil
}

// isPartial determines if we are in a first-pass or not
func (funcs goTemplateFuncs) isPartial() bool {
	return funcs.pass == 0
}
