package templates

import (
	"cmp"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"text/template"

	"golang.org/x/mod/semver"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

func TypescriptTemplateFuncs(
	schemaVersion string,
	cfg generator.Config,
) template.FuncMap {
	return typescriptTemplateFuncs{
		cfg:           cfg,
		schemaVersion: schemaVersion,
	}.FuncMap()
}

type typescriptTemplateFuncs struct {
	schemaVersion string
	cfg           generator.Config
}

func (funcs typescriptTemplateFuncs) FuncMap() template.FuncMap {
	formatTypeFunc := &FormatTypeFunc{
		formatNameFunc: funcs.formatName,
	}
	commonFunc := generator.NewCommonFunctions(funcs.schemaVersion, formatTypeFunc)
	return template.FuncMap{
		"FormatArgType":             funcs.formatArgType(commonFunc, formatTypeFunc),
		"FormatInputValueType":      funcs.formatInputValueType(commonFunc),
		"FormatFieldOutputType":     funcs.formatFieldOutputType(commonFunc),
		"FormatFieldReturnType":     funcs.formatFieldReturnType(commonFunc),
		"CommentToLines":            funcs.commentToLines,
		"FormatDeprecation":         funcs.formatDeprecation,
		"FormatExperimental":        funcs.formatExperimental,
		"FormatReturnType":          commonFunc.FormatReturnType,
		"FormatInputType":           commonFunc.FormatInputType,
		"FormatOutputType":          commonFunc.FormatOutputType,
		"FormatEnum":                funcs.formatEnum,
		"FormatName":                funcs.formatName,
		"QueryToClient":             funcs.queryToClient,
		"GetOptionalArgs":           funcs.getOptionalArgs,
		"GetRequiredArgs":           funcs.getRequiredArgs,
		"HasPrefix":                 strings.HasPrefix,
		"PascalCase":                funcs.pascalCase,
		"IsArgOptional":             funcs.isArgOptional,
		"IsCustomScalar":            funcs.isCustomScalar,
		"IsEnum":                    funcs.isEnum,
		"IsKeyword":                 funcs.isKeyword,
		"ArgsHaveDescription":       funcs.argsHaveDescription,
		"SortInputFields":           funcs.sortInputFields,
		"SortEnumFields":            funcs.sortEnumFields,
		"ExtractEnumValue":          funcs.extractEnumValue,
		"GroupEnumByValue":          funcs.groupEnumByValue,
		"GetInputEnumValueType":     funcs.getInputEnumValueType,
		"Solve":                     funcs.solve,
		"Subtract":                  funcs.subtract,
		"ConvertID":                 commonFunc.ConvertID,
		"IsSelfChainable":           commonFunc.IsSelfChainable,
		"IsListOfObject":            commonFunc.IsListOfObject,
		"IsListOfEnum":              commonFunc.IsListOfEnum,
		"GetArrayField":             commonFunc.GetArrayField,
		"ToLowerCase":               commonFunc.ToLowerCase,
		"ToUpperCase":               commonFunc.ToUpperCase,
		"ToSingleType":              funcs.toSingleType,
		"GetEnumValues":             funcs.getEnumValues,
		"IsInterface":               funcs.isInterface,
		"CheckVersionCompatibility": commonFunc.CheckVersionCompatibility,
		"ModuleRelPath":             funcs.moduleRelPath,
		"FormatProtected":           funcs.formatProtected,
		"IsClientOnly":              funcs.isClientOnly,
		"Dependencies":              funcs.Dependencies,
		"HasLocalDependencies":      funcs.HasLocalDependencies,
		"IsBundle":                  funcs.isBundle,
		"LegacyTypeScriptSDKCompat": funcs.legacyTypeScriptSDKCompat,
		"LegacyIDableTypes":         funcs.legacyIDableTypes,
		"LegacyIDName":              funcs.legacyIDName,
		"LegacyLoadFromIDName":      funcs.legacyLoadFromIDName,
	}
}

// legacyTypeScriptSDKCompatCutoverVersion is the first engine version whose
// TypeScript SDK surface is generated with unified ID-only re-entry and
// first-class interface client types. The -0 prerelease floor intentionally
// treats v0.21.0 dev builds as post-cutover while keeping v0.20.x modules in
// legacy mode.
const legacyTypeScriptSDKCompatCutoverVersion = "v0.21.0-0"

func (funcs typescriptTemplateFuncs) legacyTypeScriptSDKCompat() bool {
	if funcs.schemaVersion == "" || !semver.IsValid(funcs.schemaVersion) {
		return false
	}
	return semver.Compare(funcs.schemaVersion, legacyTypeScriptSDKCompatCutoverVersion) < 0
}

// isInterface checks if the type is a GraphQL interface.
func (funcs typescriptTemplateFuncs) isInterface(t *introspection.Type) bool {
	return t.Kind == introspection.TypeKindInterface
}

// formatArgType returns a function that formats an argument type,
// using @expectedType directive for ID scalars.
func (funcs typescriptTemplateFuncs) formatArgType(
	commonFunc *generator.CommonFunctions,
	_ *FormatTypeFunc,
) func(arg introspection.InputValue, scopes ...string) (string, error) {
	return func(arg introspection.InputValue, scopes ...string) (string, error) {
		expectedType := arg.Directives.ExpectedType()
		if expectedType != "" {
			scope := strings.Join(scopes, "")
			if scope != "" {
				scope += "."
			}
			// Check if it's a list type by walking through the TypeRef
			representation := ""
			isList := false
			for ref := arg.TypeRef; ref != nil; ref = ref.OfType {
				if ref.Kind == introspection.TypeKindList {
					isList = true
				}
			}
			representation += scope + funcs.formatName(expectedType)
			if isList {
				representation += "[]"
			}
			return representation, nil
		}
		return commonFunc.FormatInputType(arg.TypeRef, scopes...)
	}
}

// formatInputValueType returns a function that formats input values. Arguments
// named id stay on the ID scalar surface in modern mode, and become typed
// legacy FooID aliases in legacy mode when @expectedType identifies a concrete
// object/interface.
func (funcs typescriptTemplateFuncs) formatInputValueType(
	commonFunc *generator.CommonFunctions,
) func(arg introspection.InputValue, scopes ...string) (string, error) {
	formatArgType := funcs.formatArgType(commonFunc, &FormatTypeFunc{formatNameFunc: funcs.formatName})
	return func(arg introspection.InputValue, scopes ...string) (string, error) {
		if arg.Name == "id" {
			if funcs.legacyTypeScriptSDKCompat() {
				if expectedType := arg.Directives.ExpectedType(); expectedType != "" && expectedType != "Node" && !strings.HasPrefix(expectedType, "_") {
					representation := funcs.scoped(scopes...) + funcs.legacyIDName(expectedType)
					if arg.TypeRef.IsList() {
						representation += "[]"
					}
					return representation, nil
				}
			}
			return commonFunc.FormatOutputType(arg.TypeRef, scopes...)
		}
		return formatArgType(arg, scopes...)
	}
}

// formatFieldOutputType returns the raw response value type for a field. Legacy
// ID fields use old FooID aliases so generated caches/constructors match the
// pre-cutover public surface.
func (funcs typescriptTemplateFuncs) formatFieldOutputType(
	commonFunc *generator.CommonFunctions,
) func(field introspection.Field, scopes ...string) (string, error) {
	return func(field introspection.Field, scopes ...string) (string, error) {
		if funcs.legacyTypeScriptSDKCompat() && field.TypeRef.IsScalar() {
			if expectedType := funcs.fieldExpectedIDType(field); expectedType != "" {
				return funcs.scoped(scopes...) + funcs.legacyIDName(expectedType), nil
			}
		}
		return commonFunc.FormatOutputType(field.TypeRef, scopes...)
	}
}

// formatFieldReturnType returns the public method return type for a field. ID
// fields that are object re-entry points remain converted to their object type;
// plain ID fields use legacy FooID aliases only in legacy mode.
func (funcs typescriptTemplateFuncs) formatFieldReturnType(
	commonFunc *generator.CommonFunctions,
) func(field introspection.Field, scopes ...string) (string, error) {
	return func(field introspection.Field, scopes ...string) (string, error) {
		if commonFunc.ConvertID(field) {
			return commonFunc.FormatReturnType(field, scopes...)
		}
		if funcs.legacyTypeScriptSDKCompat() && field.TypeRef.IsScalar() {
			if expectedType := funcs.fieldExpectedIDType(field); expectedType != "" {
				return funcs.scoped(scopes...) + funcs.legacyIDName(expectedType), nil
			}
		}
		return commonFunc.FormatReturnType(field, scopes...)
	}
}

func (funcs typescriptTemplateFuncs) scoped(scopes ...string) string {
	scope := strings.Join(scopes, "")
	if scope != "" {
		scope += "."
	}
	return scope
}

func (funcs typescriptTemplateFuncs) fieldExpectedIDType(field introspection.Field) string {
	if !field.TypeRef.IsScalar() {
		return ""
	}
	ref := field.TypeRef
	if ref.Kind == introspection.TypeKindNonNull {
		ref = ref.OfType
	}
	if ref.Kind != introspection.TypeKindScalar || ref.Name != "ID" {
		return ""
	}
	if expectedType := field.Directives.ExpectedType(); expectedType != "" && expectedType != "Node" && !strings.HasPrefix(expectedType, "_") {
		return expectedType
	}
	if field.Name == "id" && field.ParentObject != nil && field.ParentObject.Name != "Node" && !strings.HasPrefix(field.ParentObject.Name, "_") {
		return field.ParentObject.Name
	}
	return ""
}

// pascalCase change a type name into pascalCase
func (funcs typescriptTemplateFuncs) pascalCase(name string) string {
	return strcase.ToCamel(name)
}

// solve checks if a field is solvable.
func (funcs typescriptTemplateFuncs) solve(field introspection.Field) bool {
	if field.TypeRef == nil {
		return false
	}
	return field.TypeRef.IsScalar() || field.TypeRef.IsList()
}

// subtract subtract integer a with integer b.
func (funcs typescriptTemplateFuncs) subtract(a, b int) int {
	return a - b
}

// commentToLines split a string by line breaks to be used in comments
func (funcs typescriptTemplateFuncs) commentToLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return []string{}
	}

	// Escape */ to prevent premature closure of JSDoc block comments
	s = strings.ReplaceAll(s, "*/", `*\/`)

	split := strings.Split(s, "\n")
	return split
}

// format the deprecation reason
// Example: `Replaced by @foo.` -> `// Replaced by Foo\n`
func (funcs typescriptTemplateFuncs) formatDeprecation(s string) []string {
	return funcs.formatHelper("deprecated", s)
}

func (funcs typescriptTemplateFuncs) formatExperimental(_ string) []string {
	return funcs.formatHelper("experimental", "")
}

func (funcs typescriptTemplateFuncs) formatHelper(name string, s string) []string {
	r := regexp.MustCompile("`[a-zA-Z0-9_]+`")
	matches := r.FindAllString(s, -1)
	for _, match := range matches {
		replacement := strings.TrimPrefix(match, "`")
		replacement = strings.TrimSuffix(replacement, "`")
		replacement = funcs.formatName(replacement)
		s = strings.ReplaceAll(s, match, replacement)
	}
	return funcs.commentToLines("@" + name + " " + s)
}

// isCustomScalar checks if the type is actually custom.
func (funcs typescriptTemplateFuncs) isCustomScalar(t *introspection.Type) bool {
	switch introspection.Scalar(t.Name) {
	case introspection.ScalarString, introspection.ScalarInt, introspection.ScalarFloat, introspection.ScalarBoolean:
		return false
	default:
		return t.Kind == introspection.TypeKindScalar
	}
}

// isEnum checks if the type is actually custom.
func (funcs typescriptTemplateFuncs) isEnum(t *introspection.Type) bool {
	return t.Kind == introspection.TypeKindEnum &&
		// We ignore the internal GraphQL enums
		!strings.HasPrefix(t.Name, "_")
}

func (funcs typescriptTemplateFuncs) isKeyword(s string) bool {
	_, isKeyword := jsKeywords[strings.ToLower(s)]

	return isKeyword
}

// formatName formats a GraphQL name (e.g. object, field, arg) into a TS
// equivalent, avoiding collisions with reserved words.
func (funcs typescriptTemplateFuncs) formatName(s string) string {
	if _, isKeyword := jsKeywords[strings.ToLower(s)]; isKeyword {
		// NB: this is case-insensitive; in JS, both function and Function cause
		// problems (one straight up doesn't parse, the other causes lint errors)
		return s + "_"
	}
	return s
}

func (funcs typescriptTemplateFuncs) queryToClient(s string) string {
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
func (funcs typescriptTemplateFuncs) formatEnum(s string) string {
	return strcase.ToCamel(s)
}

// isArgOptional checks if some arg are optional.
// They are, if all of there InputValues are optional.
func (funcs typescriptTemplateFuncs) isArgOptional(values introspection.InputValues) bool {
	for _, v := range values {
		if !v.IsOptional() {
			return false
		}
	}
	return true
}

func (funcs typescriptTemplateFuncs) splitRequiredOptionalArgs(values introspection.InputValues) (required introspection.InputValues, optionals introspection.InputValues) {
	for i, v := range values {
		if !v.IsOptional() {
			continue
		}

		return values[:i], values[i:]
	}
	return values, nil
}

func (funcs typescriptTemplateFuncs) getEnumValues(values introspection.InputValues) introspection.InputValues {
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

func (funcs typescriptTemplateFuncs) getInputEnumValueType(enum introspection.InputValue) string {
	if enum.TypeRef.OfType != nil && enum.TypeRef.OfType.Kind == introspection.TypeKindEnum {
		return enum.TypeRef.OfType.Name
	}

	return enum.TypeRef.Name
}

func (funcs typescriptTemplateFuncs) getRequiredArgs(values introspection.InputValues) introspection.InputValues {
	required, _ := funcs.splitRequiredOptionalArgs(values)
	return required
}

func (funcs typescriptTemplateFuncs) getOptionalArgs(values introspection.InputValues) introspection.InputValues {
	_, optional := funcs.splitRequiredOptionalArgs(values)
	return optional
}

func (funcs typescriptTemplateFuncs) sortInputFields(s []introspection.InputValue) []introspection.InputValue {
	sort.SliceStable(s, func(i, j int) bool {
		return s[i].Name < s[j].Name
	})
	return s
}

func (funcs typescriptTemplateFuncs) sortEnumFields(s []introspection.EnumValue) []introspection.EnumValue {
	copy := slices.Clone(s)

	slices.SortStableFunc(copy, func(x, y introspection.EnumValue) int {
		return cmp.Compare(strcase.ToCamel(x.Name), strcase.ToCamel(y.Name))
	})

	copy = slices.CompactFunc(copy, func(x, y introspection.EnumValue) bool {
		return strcase.ToCamel(x.Name) == strcase.ToCamel(y.Name)
	})

	return copy
}

func (funcs typescriptTemplateFuncs) extractEnumValue(enum introspection.EnumValue) string {
	return enum.Directives.EnumValue()
}

// groupEnumByValue returns a list of lists of enums, grouped by similar enum value.
//
// Additionally, enum names within a single value are removed (which would
// result in duplicate codegen).
func (funcs typescriptTemplateFuncs) groupEnumByValue(s []introspection.EnumValue) [][]introspection.EnumValue {
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

func (funcs typescriptTemplateFuncs) argsHaveDescription(values introspection.InputValues) bool {
	for _, o := range values {
		if strings.TrimSpace(o.Description) != "" {
			return true
		}
	}

	return false
}

func (funcs typescriptTemplateFuncs) toSingleType(value string) string {
	return value[:len(value)-2]
}

func (funcs typescriptTemplateFuncs) moduleRelPath(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}

	moduleParentPath := ""
	if funcs.cfg.ModuleConfig != nil {
		moduleParentPath = funcs.cfg.ModuleConfig.ModuleParentPath
	}

	return filepath.Join(
		// Path to the root of this module (since we're at the codegen root sdk/src/api/).
		"../../../",
		// Path to the module's context directory.
		moduleParentPath,
		// Path from the context directory to the target path.
		path,
	)
}

func (funcs typescriptTemplateFuncs) formatProtected(s string) string {
	return strings.TrimSuffix(s, "_")
}

func (funcs typescriptTemplateFuncs) legacyIDName(typeName string) string {
	return typeName + "ID"
}

func (funcs typescriptTemplateFuncs) legacyLoadFromIDName(typeName string) string {
	return funcs.formatName("load" + typeName + "FromID")
}

func (funcs typescriptTemplateFuncs) legacyIDableTypes() []*introspection.Type {
	if !funcs.legacyTypeScriptSDKCompat() {
		return nil
	}
	schema := generator.GetSchema()
	if schema == nil {
		return nil
	}
	var types []*introspection.Type
	for _, t := range schema.Types {
		if t == nil || t.Name == "Node" || strings.HasPrefix(t.Name, "_") {
			continue
		}
		if t.Kind != introspection.TypeKindObject && t.Kind != introspection.TypeKindInterface {
			continue
		}
		idName := funcs.legacyIDName(t.Name)
		if schema.Types.Get(idName) != nil {
			continue
		}
		if !slices.ContainsFunc(t.Fields, func(field *introspection.Field) bool {
			return field.Name == "id" && field.TypeRef != nil && field.TypeRef.IsScalar()
		}) {
			continue
		}
		types = append(types, t)
	}
	return types
}

func (funcs typescriptTemplateFuncs) isClientOnly() bool {
	return funcs.cfg.ClientConfig != nil
}

func (funcs typescriptTemplateFuncs) Dependencies() []generator.ModuleSourceDependency {
	return funcs.cfg.ClientConfig.ModuleDependencies
}

func (funcs typescriptTemplateFuncs) HasLocalDependencies() bool {
	for _, dep := range funcs.cfg.ClientConfig.ModuleDependencies {
		if dep.Kind == "LOCAL_SOURCE" {
			return true
		}
	}

	return false
}

func (funcs typescriptTemplateFuncs) isBundle() bool {
	return funcs.cfg.Bundle
}
