package templates

import (
	"context"
	"fmt"
	"go/token"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/iancoleman/strcase"
	"golang.org/x/tools/go/packages"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core/modules"
)

func GoTemplateFuncs(
	ctx context.Context,
	schema *introspection.Schema,
	module *modules.Config,
	pkg *packages.Package,
	fset *token.FileSet,
) template.FuncMap {
	return goTemplateFuncs{
		CommonFunctions: generator.NewCommonFunctions(&FormatTypeFunc{}),
		ctx:             ctx,
		module:          module,
		modulePkg:       pkg,
		moduleFset:      fset,
		schema:          schema,
	}.FuncMap()
}

type goTemplateFuncs struct {
	*generator.CommonFunctions
	ctx        context.Context
	module     *modules.Config
	modulePkg  *packages.Package
	moduleFset *token.FileSet
	schema     *introspection.Schema
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
		"IsIDableObject":   funcs.IsIDableObject,
		"InnerType":        funcs.InnerType,
		"ObjectName":       funcs.ObjectName,

		// go specific
		"Comment":                 funcs.comment,
		"FormatDeprecation":       funcs.formatDeprecation,
		"FormatName":              formatName,
		"FormatEnum":              funcs.formatEnum,
		"SortEnumFields":          funcs.sortEnumFields,
		"FieldOptionsStructName":  funcs.fieldOptionsStructName,
		"FieldFunction":           funcs.fieldFunction,
		"IsEnum":                  funcs.isEnum,
		"IsPointer":               funcs.isPointer,
		"FormatArrayField":        funcs.formatArrayField,
		"FormatArrayToSingleType": funcs.formatArrayToSingleType,
		"IsModuleCode":            funcs.isModuleCode,
		"ModuleMainSrc":           funcs.moduleMainSrc,
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

// fieldFunction converts a field into a function signature
// Example: `contents: String!` -> `func (r *File) Contents(ctx context.Context) (string, error)`
func (funcs goTemplateFuncs) fieldFunction(f introspection.Field, topLevel bool, scopes ...string) (string, error) {
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
		if arg.TypeRef.IsOptional() {
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
	if f.Args.HasOptionals() {
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
	if f.TypeRef.IsScalar() || f.TypeRef.IsList() {
		retType = fmt.Sprintf("(%s, error)", retType)
	} else {
		retType = "*" + retType
	}
	signature += " " + retType

	return signature, nil
}
