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
	fullSchema *introspection.Schema,
	schemaVersion string,
	cfg generator.Config,
	pkg *packages.Package,
	fset *token.FileSet,
	pass int,
) template.FuncMap {
	if fullSchema == nil {
		fullSchema = schema
	}
	return goTemplateFuncs{
		CommonFunctions: generator.NewCommonFunctions(schemaVersion, &FormatTypeFunc{}),
		ctx:             ctx,
		cfg:             cfg,
		modulePkg:       pkg,
		moduleFset:      fset,
		schema:          schema,
		fullSchema:      fullSchema,
		schemaVersion:   schemaVersion,
		pass:            pass,
	}.FuncMap()
}

type goTemplateFuncs struct {
	*generator.CommonFunctions
	ctx        context.Context
	cfg        generator.Config
	modulePkg  *packages.Package
	moduleFset *token.FileSet
	schema     *introspection.Schema
	// fullSchema is the complete schema including all dependency types. It is
	// used for type lookups (e.g. resolving dep-contributed enums in module
	// code) while schema may be a filtered subset used for code rendering.
	fullSchema    *introspection.Schema
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
		"LegacyGoSDKCompat":         funcs.legacyGoSDKCompat,

		// arg formatting with directive support
		"FormatArgType": funcs.formatArgType,

		// interface support
		"IsInterfaceType":          funcs.isInterfaceType,
		"IsInterfaceRef":           funcs.isInterfaceRef,
		"IsListOfInterface":        funcs.isListOfInterface,
		"InterfaceClientName":      funcs.interfaceClientName,
		"InterfaceReturnType":      funcs.interfaceReturnType,
		"InterfaceListResultType":  funcs.interfaceListResultType,
		"PossibleTypes":            funcs.possibleTypes,
		"ImplementedInterfaces":    funcs.implementedInterfaces,
		"InterfaceMethodSignature": funcs.interfaceMethodSignature,
		"InterfaceClientMethod":    funcs.interfaceClientMethod,

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
		"IsExtendableType":        funcs.isExtendableType,
		"FullSchemaTypes":         funcs.fullSchemaTypes,
		"HasIDField":              funcs.hasIDField,
		"json":                    funcs.json,
	}
}

// legacyGoSDKCompatCutoverVersion is the first engine version whose Go SDK
// surface is generated with unified ID-only re-entry and first-class interface
// client types. The -0 prerelease floor intentionally treats v0.21.0 dev
// builds as post-cutover while keeping all v0.20.x modules in legacy mode.
const legacyGoSDKCompatCutoverVersion = "v0.21.0-0"

func (funcs goTemplateFuncs) legacyGoSDKCompat() bool {
	if funcs.schemaVersion == "" || funcs.CommonFunctions == nil {
		return false
	}
	return !funcs.CheckVersionCompatibility(legacyGoSDKCompatCutoverVersion)
}

// fullSchemaTypes returns all types from the full schema, including dependency
// types. Unlike .Types (which is bound to the filtered core schema), this
// includes types contributed by dependency modules.
func (funcs goTemplateFuncs) fullSchemaTypes() []*introspection.Type {
	return funcs.fullSchema.Visit()
}

func (goTemplateFuncs) isExtendableType(t introspection.Type) bool {
	return slices.Contains(introspection.ExtendableTypes, t.Name)
}

func (goTemplateFuncs) hasIDField(t introspection.Type) bool {
	for _, field := range t.Fields {
		if field.Name == "id" && field.TypeRef.IsScalar() {
			return true
		}
	}
	return false
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
	representation, err := funcs.formatArgType(t)
	return strings.Index(representation, "*") == 0, err
}

// formatName formats a GraphQL name (e.g. object, field, arg) into a Go equivalent
// Example: `fooId` -> `FooID`
func formatName(s string) string {
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

		// For node(id:) on Query, keep the arg as the raw ID type.
		if f.ParentObject.Name == generator.QueryStructName && arg.Name == "id" {
			outType, err := funcs.FormatOutputType(arg.TypeRef, scopes...)
			if err != nil {
				return "", err
			}
			args = append(args, fmt.Sprintf("%s %s", arg.Name, outType))
		} else {
			inType, err := funcs.formatArgType(arg, scopes...)
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
	if funcs.legacyGoSDKCompat() && funcs.isListOfInterface(f.TypeRef) {
		retType, err = funcs.interfaceListResultType(f.TypeRef, scopes...)
		if err != nil {
			return "", err
		}
	}
	convertID := funcs.ConvertID(f)
	switch {
	case supportsVoid && f.TypeRef.IsVoid():
		retType = "error"
	case convertID:
		// ConvertID fields return the parent object type as a pointer.
		retType = fmt.Sprintf("(*%s, error)", retType)
	case f.TypeRef.IsScalar() || f.TypeRef.IsList():
		retType = fmt.Sprintf("(%s, error)", retType)
	case funcs.isInterfaceRef(f.TypeRef):
		retType, err = funcs.interfaceReturnType(funcs.InnerType(f.TypeRef).Name, scopes...)
		if err != nil {
			return "", err
		}
	default:
		retType = "*" + retType
	}
	signature += " " + retType

	return signature, nil
}

// isInterfaceType returns true if the given introspection type is an INTERFACE.
func (funcs goTemplateFuncs) isInterfaceType(t introspection.Type) bool {
	return t.Kind == introspection.TypeKindInterface
}

// isInterfaceRef returns true if the given type ref points to a single INTERFACE kind
// type (not a list of interfaces). Only unwraps NonNull wrappers.
func (funcs goTemplateFuncs) isInterfaceRef(t *introspection.TypeRef) bool {
	ref := t
	for ref != nil {
		switch ref.Kind {
		case introspection.TypeKindNonNull:
			ref = ref.OfType
		case introspection.TypeKindInterface:
			return true
		default:
			return false
		}
	}
	return false
}

// isListOfInterface returns true if the type ref is a list whose element is an interface.
func (funcs goTemplateFuncs) isListOfInterface(t *introspection.TypeRef) bool {
	// Unwrap NonNull -> List -> NonNull? -> Interface
	ref := t
	if ref.Kind == introspection.TypeKindNonNull {
		ref = ref.OfType
	}
	if ref.Kind != introspection.TypeKindList {
		return false
	}
	ref = ref.OfType
	if ref.Kind == introspection.TypeKindNonNull {
		ref = ref.OfType
	}
	return ref.Kind == introspection.TypeKindInterface
}

// interfaceClientName returns the query-builder struct name for an interface.
// e.g. "Duck" -> "DuckClient". In legacy Go SDK compatibility mode,
// non-Node interfaces use the pre-cutover wrapper struct name directly.
func (funcs goTemplateFuncs) interfaceClientName(name string) string {
	if funcs.legacyGoSDKCompat() && name != "Node" {
		return formatName(name)
	}
	return formatName(name) + "Client"
}

func (funcs goTemplateFuncs) interfaceReturnType(name string, scopes ...string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("missing interface name")
	}
	scope := strings.Join(scopes, "")
	if scope != "" {
		scope += "."
	}
	if funcs.legacyGoSDKCompat() && name != "Node" {
		return "*" + scope + formatName(name), nil
	}
	return scope + formatName(name), nil
}

func (funcs goTemplateFuncs) interfaceListResultType(t *introspection.TypeRef, scopes ...string) (string, error) {
	if funcs.legacyGoSDKCompat() && funcs.isListOfInterface(t) {
		inner := funcs.InnerType(t)
		if inner.Name != "Node" {
			scope := strings.Join(scopes, "")
			if scope != "" {
				scope += "."
			}
			return "[]*" + scope + formatName(inner.Name), nil
		}
	}
	return funcs.FormatOutputType(t, scopes...)
}

// possibleTypes returns the possible concrete types for an interface type,
// filtering out interface entries so that only object types are returned.
// Interfaces may appear in PossibleTypes due to interface-extends-interface
// registrations, but codegen (e.g. Concrete() switch) only needs objects.
func (funcs goTemplateFuncs) possibleTypes(t introspection.Type) []*introspection.Type {
	var result []*introspection.Type
	for _, pt := range t.PossibleTypes {
		if pt.Kind == introspection.TypeKindInterface {
			continue
		}
		result = append(result, pt)
	}
	return result
}

// implementedInterfaces returns the interfaces that an object type implements,
// filtered to only include non-built-in interfaces (i.e. not "Object").
func (funcs goTemplateFuncs) implementedInterfaces(t introspection.Type) []*introspection.Type {
	var result []*introspection.Type
	for _, iface := range t.Interfaces {
		if iface.Name == "Object" {
			continue
		}
		result = append(result, iface)
	}
	return result
}

// interfaceMethodSignature generates a Go interface method signature for a field.
// e.g. "Quack(ctx context.Context) (string, error)"
func (funcs goTemplateFuncs) interfaceMethodSignature(f introspection.Field) (string, error) {
	sig := formatName(f.Name)

	args := []string{}
	if f.TypeRef.IsScalar() || f.TypeRef.IsList() {
		args = append(args, "ctx context.Context")
	}
	for _, arg := range f.Args {
		if funcs.isArgOptional(arg) {
			continue
		}
		inType, err := funcs.formatArgType(arg)
		if err != nil {
			return "", err
		}
		args = append(args, fmt.Sprintf("%s %s", arg.Name, inType))
	}
	if funcs.hasOptionals(f.Args) {
		args = append(args, fmt.Sprintf("opts ...%s", funcs.fieldOptionsStructName(f)))
	}
	sig += "(" + strings.Join(args, ", ") + ")"

	retType, err := funcs.FormatReturnType(f)
	if err != nil {
		return "", err
	}
	if funcs.legacyGoSDKCompat() && funcs.isListOfInterface(f.TypeRef) {
		retType, err = funcs.interfaceListResultType(f.TypeRef)
		if err != nil {
			return "", err
		}
	}
	supportsVoid := funcs.CheckVersionCompatibility("v0.12.0")
	convertID := funcs.ConvertID(f)
	switch {
	case supportsVoid && f.TypeRef.IsVoid():
		sig += " error"
	case convertID:
		retType, err = funcs.interfaceReturnType(f.ParentObject.Name)
		if err != nil {
			return "", err
		}
		sig += fmt.Sprintf(" (%s, error)", retType)
	case f.TypeRef.IsScalar() || f.TypeRef.IsList():
		sig += fmt.Sprintf(" (%s, error)", retType)
	case funcs.isInterfaceRef(f.TypeRef):
		retType, err = funcs.interfaceReturnType(funcs.InnerType(f.TypeRef).Name)
		if err != nil {
			return "", err
		}
		sig += " " + retType
	default:
		sig += " *" + retType
	}
	return sig, nil
}

// interfaceClientMethod generates a method signature for the interface's
// query-builder struct. e.g.:
//
//	func (r *duckClient) Quack(ctx context.Context) (string, error)
func (funcs goTemplateFuncs) interfaceClientMethod(ifaceName string, f introspection.Field) (string, error) {
	clientName := funcs.interfaceClientName(ifaceName)
	sig := "func (r *" + clientName + ") " + formatName(f.Name)

	args := []string{}
	if f.TypeRef.IsScalar() || f.TypeRef.IsList() {
		args = append(args, "ctx context.Context")
	}
	for _, arg := range f.Args {
		if funcs.isArgOptional(arg) {
			continue
		}
		inType, err := funcs.formatArgType(arg)
		if err != nil {
			return "", err
		}
		args = append(args, fmt.Sprintf("%s %s", arg.Name, inType))
	}
	if funcs.hasOptionals(f.Args) {
		args = append(args, fmt.Sprintf("opts ...%s", funcs.fieldOptionsStructName(f)))
	}
	sig += "(" + strings.Join(args, ", ") + ")"

	retType, err := funcs.FormatReturnType(f)
	if err != nil {
		return "", err
	}
	if funcs.legacyGoSDKCompat() && funcs.isListOfInterface(f.TypeRef) {
		retType, err = funcs.interfaceListResultType(f.TypeRef)
		if err != nil {
			return "", err
		}
	}
	supportsVoid := funcs.CheckVersionCompatibility("v0.12.0")
	convertID := funcs.ConvertID(f)
	switch {
	case supportsVoid && f.TypeRef.IsVoid():
		sig += " error"
	case convertID:
		retType, err = funcs.interfaceReturnType(f.ParentObject.Name)
		if err != nil {
			return "", err
		}
		sig += fmt.Sprintf(" (%s, error)", retType)
	case f.TypeRef.IsScalar() || f.TypeRef.IsList():
		sig += fmt.Sprintf(" (%s, error)", retType)
	case funcs.isInterfaceRef(f.TypeRef):
		retType, err = funcs.interfaceReturnType(funcs.InnerType(f.TypeRef).Name)
		if err != nil {
			return "", err
		}
		sig += " " + retType
	default:
		sig += " *" + retType
	}
	return sig, nil
}

// formatArgType formats an argument's type, using the @expectedType directive
// when the arg is an ID scalar. This replaces the old FooID -> *Foo conversion.
func (funcs goTemplateFuncs) formatArgType(arg introspection.InputValue, scopes ...string) (string, error) {
	expectedType := arg.Directives.ExpectedType()
	if expectedType != "" {
		// This is an ID arg with an @expectedType directive.
		// Format it as the expected type (pointer for objects, value for interfaces).
		scope := strings.Join(scopes, "")
		if scope != "" {
			scope += "."
		}
		var baseType string
		schema := generator.GetSchema()
		if schema != nil {
			schemaType := schema.Types.Get(expectedType)
			if schemaType != nil && schemaType.Kind == introspection.TypeKindInterface {
				if funcs.legacyGoSDKCompat() && expectedType != "Node" {
					baseType = "*" + scope + formatName(expectedType)
				} else {
					baseType = scope + formatName(expectedType)
				}
			}
		}
		if baseType == "" {
			baseType = "*" + scope + formatName(expectedType)
		}
		// Wrap in [] for list types.
		ref := arg.TypeRef
		if ref != nil && ref.Kind == introspection.TypeKindNonNull {
			ref = ref.OfType
		}
		if ref != nil && ref.Kind == introspection.TypeKindList {
			return "[]" + baseType, nil
		}
		return baseType, nil
	}
	return funcs.FormatInputType(arg.TypeRef, scopes...)
}

// isPartial determines if we are in a first-pass or not
func (funcs goTemplateFuncs) isPartial() bool {
	return funcs.pass == 0
}
