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
	fullSchema *introspection.Schema,
	selfModule string,
	cfg generator.Config,
) template.FuncMap {
	return typescriptTemplateFuncs{
		cfg:           cfg,
		schemaVersion: schemaVersion,
		fullSchema:    fullSchema,
		selfModule:    selfModule,
		memo:          &templateMemo{},
	}.FuncMap()
}

// templateMemo caches results that are constant across every file rendered from
// one schema. It is held by pointer so the cache survives the value-copies the
// template engine makes of the funcs receiver. Rendering is sequential, so no
// locking is needed.
type templateMemo struct {
	depExports    []DependencyExport
	depExportsSet bool
}

type typescriptTemplateFuncs struct {
	schemaVersion string
	cfg           generator.Config
	memo          *templateMemo

	// fullSchema is the complete, unfiltered schema (all dependency types
	// included). The per-file render data may carry a filtered schema (the
	// core schema for client.gen.ts, or a single dep's schema), so the
	// dependency-splitting helpers consult fullSchema to enumerate deps and
	// to decide which types belong to client.gen.ts vs. a per-dep file.
	fullSchema *introspection.Schema

	// selfModule is the name of the module the client is being generated for.
	// Only *dependencies* are split into their own <dep>.gen.ts files; the
	// module's own types stay in client.gen.ts, so self is excluded from the
	// dependency enumeration.
	selfModule string
}

// DependencyModules returns the schema's dependency module names with the
// module being generated for (self) removed: only dependencies are split into
// their own files. Names are compared kebab-cased to tolerate casing/separator
// differences between sourceMap module names and the configured name. This is
// the single source of truth shared by the generator (which filters the schema)
// and the templates (which enumerate the per-dep files).
func DependencyModules(schema *introspection.Schema, self string) []string {
	if schema == nil {
		return nil
	}
	all := schema.DependencyNames()
	out := make([]string, 0, len(all))
	for _, name := range all {
		if isSameModule(name, self) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// dependencyNames returns the modules whose types are split into their own
// files: every module in the schema except the one being generated for.
func (funcs typescriptTemplateFuncs) dependencyNames() []string {
	return DependencyModules(funcs.fullSchema, funcs.selfModule)
}

// isSameModule compares two module names tolerant of casing/separator
// differences (sourceMap module names vs. the configured module name).
func isSameModule(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strcase.ToKebab(a) == strcase.ToKebab(b)
}

func (funcs typescriptTemplateFuncs) FuncMap() template.FuncMap {
	formatTypeFunc := &FormatTypeFunc{
		formatNameFunc: funcs.formatName,
	}
	commonFunc := generator.NewCommonFunctions(funcs.schemaVersion, formatTypeFunc)
	return template.FuncMap{
		"FormatFieldOutputType":     funcs.formatFieldOutputType(commonFunc),
		"FormatFieldReturnType":     funcs.formatFieldReturnType(commonFunc),
		"CommentToLines":            funcs.commentToLines,
		"FormatDeprecation":         funcs.formatDeprecation,
		"FormatExperimental":        funcs.formatExperimental,
		"FormatReturnType":          commonFunc.FormatReturnType,
		"FormatInputType":           funcs.formatInputType(commonFunc),
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
		"IsListOfInterface":         funcs.isListOfInterface,
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
		// Dependency splitting: render each dependency's types into its own
		// <dep>.gen.ts file plus prototype augmentations on the extendable
		// types (Client/Binding/Env).
		"DependencyFiles":      funcs.dependencyFiles,
		"DependencyExports":    funcs.dependencyExports,
		"DepFileName":          funcs.depFileName,
		"CoreTypeNames":        funcs.coreTypeNames,
		"CoreValueNames":       funcs.coreValueNames,
		"ExtendableClassNames": funcs.extendableClassNames,
		"IsExtendableType":     funcs.isExtendableType,
		"AugmentFnName":        augmentFnName,
		"Augmentation":         augmentation,
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

// formatInputType returns a function that formats input values.
func (funcs typescriptTemplateFuncs) formatInputType(
	commonFunc *generator.CommonFunctions,
) func(arg introspection.InputValue, scopes ...string) (string, error) {
	return func(arg introspection.InputValue, scopes ...string) (string, error) {
		if expectedType := arg.Directives.ExpectedType(); expectedType != "" {
			if arg.Name == "id" && funcs.legacyTypeScriptSDKCompat() && expectedType != "Node" && !strings.HasPrefix(expectedType, "_") {
				representation := funcs.scoped(scopes...) + funcs.legacyIDName(expectedType)
				if arg.TypeRef != nil && arg.TypeRef.IsList() {
					representation += "[]"
				}
				return representation, nil
			}
			representation := funcs.scoped(scopes...) + funcs.formatName(expectedType)
			if arg.TypeRef != nil && arg.TypeRef.IsList() {
				representation += "[]"
			}
			return representation, nil
		}
		if arg.Name == "id" {
			return commonFunc.FormatOutputType(arg.TypeRef, scopes...)
		}
		return commonFunc.FormatInputType(arg.TypeRef, scopes...)
	}
}

func (funcs typescriptTemplateFuncs) isListOfInterface(t *introspection.TypeRef) bool {
	if t == nil || !t.IsList() {
		return false
	}
	for ref := t; ref != nil; ref = ref.OfType {
		switch ref.Kind {
		case introspection.TypeKindNonNull, introspection.TypeKindList:
			continue
		default:
			return ref.Kind == introspection.TypeKindInterface
		}
	}
	return false
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

// legacyIDableTypes returns, for the file currently being rendered, the types
// whose <Name>ID alias must be declared (legacy mode only). It is scoped to the
// file's own types so each <dep>.gen.ts emits only its dependency's ID aliases:
// reading the global schema instead would re-emit every core <Name>ID in every
// dep file, duplicating client.gen.ts's aliases (TS2308 via `export *`).
func (funcs typescriptTemplateFuncs) legacyIDableTypes(fileTypes []*introspection.Type) []*introspection.Type {
	if !funcs.legacyTypeScriptSDKCompat() {
		return nil
	}
	schema := generator.GetSchema()
	if schema == nil {
		return nil
	}
	var types []*introspection.Type
	for _, t := range fileTypes {
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

// DependencyExport describes, for a single dependency, the per-dep generated
// file and the TypeScript identifiers it contributes. client.gen.ts uses this
// to emit named imports (so inline references like `new Hello(ctx)` resolve)
// and `export *` re-exports for downstream consumers.
type DependencyExport struct {
	// File is the kebab-cased basename (no extension) of the dep file.
	File string
	// Names are the TS identifiers (object/scalar/enum/input types plus
	// per-method Opts types) the dep file exports.
	Names []string
	// AugmentFnName is the exported function the dep file uses to attach its
	// prototype augmentations to the extendable type classes
	// (e.g. `__applyHelloAugmentations`).
	AugmentFnName string
}

// AugmentationData bundles the parent class name and a dep-contributed field
// for the augmentation sub-templates (text/template only allows a single
// positional argument).
type AugmentationData struct {
	Parent string
	Field  *introspection.Field
}

func augmentation(parent string, field *introspection.Field) AugmentationData {
	return AugmentationData{Parent: parent, Field: field}
}

// augmentFnName derives the exported augmentation function name for a dep,
// e.g. "hello" -> "__applyHelloAugmentations".
func augmentFnName(depName string) string {
	return "__apply" + strcase.ToCamel(depName) + "Augmentations"
}

// depFileName converts a module name to the kebab-cased basename used for its
// generated file, e.g. "myDep" -> "my-dep" (file "my-dep.gen.ts").
func (funcs typescriptTemplateFuncs) depFileName(moduleName string) string {
	return strcase.ToKebab(moduleName)
}

// isExtendableType reports whether the type is one of the core extendable
// types (Query/Binding/Env) that dependencies contribute fields to via
// prototype augmentation rather than re-declaring the class.
func (funcs typescriptTemplateFuncs) isExtendableType(t *introspection.Type) bool {
	if t == nil {
		return false
	}
	return slices.Contains(introspection.ExtendableTypes, t.Name)
}

// dependencyFiles returns the per-dep generated filenames (kebab-cased, no
// extension), sorted by dependency name.
func (funcs typescriptTemplateFuncs) dependencyFiles() []string {
	if funcs.fullSchema == nil {
		return nil
	}
	deps := funcs.dependencyNames()
	out := make([]string, len(deps))
	for i, d := range deps {
		out[i] = funcs.depFileName(d)
	}
	return out
}

// dependencyExports returns, for each dependency, the file basename and the
// set of TS identifiers it exports (so client.gen.ts can import + re-export
// them). The result is memoized: client.gen.ts asks for it twice (the import
// block and the footer), and each call does an Include() full-schema scan per
// dependency.
func (funcs typescriptTemplateFuncs) dependencyExports() []DependencyExport {
	if funcs.fullSchema == nil {
		return nil
	}
	if funcs.memo != nil && funcs.memo.depExportsSet {
		return funcs.memo.depExports
	}
	deps := funcs.dependencyNames()
	out := make([]DependencyExport, 0, len(deps))
	for _, dep := range deps {
		depSchema := funcs.fullSchema.Include(dep)
		out = append(out, DependencyExport{
			File:          funcs.depFileName(dep),
			Names:         funcs.exportedTypeNames(depSchema.Types, nil),
			AugmentFnName: augmentFnName(dep),
		})
	}
	if funcs.memo != nil {
		funcs.memo.depExports = out
		funcs.memo.depExportsSet = true
	}
	return out
}

// dependencyNameSet returns the dependency module names as a set, for quick
// "is this type owned by a dependency" lookups.
func (funcs typescriptTemplateFuncs) dependencyNameSet() map[string]struct{} {
	set := map[string]struct{}{}
	for _, d := range funcs.dependencyNames() {
		set[d] = struct{}{}
	}
	return set
}

// isDependencyOwned reports whether a type is contributed by one of the
// dependencies (and so lives in a <dep>.gen.ts file rather than client.gen.ts).
func (funcs typescriptTemplateFuncs) isDependencyOwned(t *introspection.Type, depSet map[string]struct{}) bool {
	if sm := t.Directives.SourceMap(); sm != nil {
		_, ok := depSet[sm.Module]
		return ok
	}
	return false
}

// isBuiltinScalar reports whether name is a GraphQL builtin scalar that maps to
// a native TS type (string/number/float/boolean) and is therefore never
// imported or exported by name.
func isBuiltinScalar(name string) bool {
	switch introspection.Scalar(name) {
	case introspection.ScalarString, introspection.ScalarInt,
		introspection.ScalarFloat, introspection.ScalarBoolean:
		return true
	}
	return false
}

// isExportableType reports whether the generated client surfaces this type by
// name — i.e. whether it can appear in a per-dep file's import and in
// client.gen.ts's re-export. It is the single predicate shared by the importing
// side (coreTypeNames/coreValueNames) and the exporting side
// (exportedTypeNames), so the two can't drift. It excludes internal
// (_-prefixed) types, builtin scalars, and the extendable types (which are
// bound from `scope` inside the augmentation function, never imported).
func (funcs typescriptTemplateFuncs) isExportableType(t *introspection.Type) bool {
	if t == nil || strings.HasPrefix(t.Name, "_") {
		return false
	}
	if slices.Contains(introspection.ExtendableTypes, t.Name) {
		return false
	}
	return !isBuiltinScalar(t.Name)
}

// collectReferencedNames returns every type name appearing in the dependency
// surface (field return types, argument types, and input fields).
func (funcs typescriptTemplateFuncs) collectReferencedNames(depTypes []*introspection.Type) map[string]struct{} {
	referenced := map[string]struct{}{}
	visit := func(ref *introspection.TypeRef) {
		for ; ref != nil; ref = ref.OfType {
			if ref.Name != "" {
				referenced[ref.Name] = struct{}{}
			}
		}
	}
	for _, t := range depTypes {
		for _, f := range t.Fields {
			visit(f.TypeRef)
			for _, a := range f.Args {
				visit(a.TypeRef)
			}
		}
		for _, in := range t.InputFields {
			visit(in.TypeRef)
		}
	}
	return referenced
}

// addLegacyIDRefs adds, in legacy mode, the <Object>ID alias names referenced
// by the dependency surface. An object's `id` field (and id-typed args) render
// as the per-type alias <Object>ID rather than the generic ID scalar, so the
// dep file imports those aliases from client.gen.ts.
func (funcs typescriptTemplateFuncs) addLegacyIDRefs(depTypes []*introspection.Type, referenced map[string]struct{}) {
	if !funcs.legacyTypeScriptSDKCompat() {
		return
	}
	objectNames := map[string]struct{}{}
	for _, t := range depTypes {
		if t.Kind == introspection.TypeKindObject {
			objectNames[t.Name] = struct{}{}
		}
	}
	for name := range referenced {
		if t := funcs.fullSchema.Types.Get(name); t != nil && t.Kind == introspection.TypeKindObject {
			objectNames[name] = struct{}{}
		}
	}
	for name := range objectNames {
		idName := funcs.legacyIDName(name)
		if funcs.fullSchema.Types.Get(idName) != nil {
			referenced[idName] = struct{}{}
		}
	}
}

// coreTypeNames returns the core (non-dependency) identifiers a per-dep file
// imports from client.gen.ts as *types only*: scalars, input objects, enum
// types, the `float` alias, and (in legacy mode) the <Object>ID aliases the dep
// references in signatures. Core *values* the dep constructs or calls (object
// classes, enum converters) are value-imported via coreValueNames instead — a
// type-only import used as a value is a hard tsc error (TS1361) and, since type
// imports are erased under ESM, a runtime ReferenceError.
func (funcs typescriptTemplateFuncs) coreTypeNames(depTypes []*introspection.Type) []string {
	if funcs.fullSchema == nil {
		return nil
	}

	depSet := funcs.dependencyNameSet()
	referenced := funcs.collectReferencedNames(depTypes)
	funcs.addLegacyIDRefs(depTypes, referenced)

	seen := map[string]struct{}{}
	var names []string
	add := func(name string) {
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for name := range referenced {
		// The Float scalar is the one builtin that renders as a TS alias
		// (`float`) declared in client.gen.ts rather than a native type, so the
		// dep file imports that alias.
		if introspection.Scalar(name) == introspection.ScalarFloat {
			add("float")
			continue
		}
		t := funcs.fullSchema.Types.Get(name)
		if t == nil || !funcs.isExportableType(t) || funcs.isDependencyOwned(t, depSet) {
			continue
		}
		// Object classes are runtime values (the bodies do `new X(ctx)`); they
		// are value-imported via coreValueNames, which also covers their use as
		// signature types.
		if t.Kind == introspection.TypeKindObject {
			continue
		}
		add(funcs.exportedTypeName(t))
	}

	sort.Strings(names)
	return names
}

// coreValueNames returns the core (non-dependency) identifiers the generated
// dependency bodies reference as runtime VALUES and therefore value-import from
// client.gen.ts: object classes they construct with `new`, and the enum
// converter functions (<Enum>ValueToName / <Enum>NameToValue) they call.
//
// Value-importing these is safe under the client.gen.ts <-> dep-file ESM cycle:
// every reference is inside a method body or the deferred augmentation
// function, never at module-evaluation time, so the binding is always
// initialized by the time it is used.
func (funcs typescriptTemplateFuncs) coreValueNames(depTypes []*introspection.Type) []string {
	if funcs.fullSchema == nil {
		return nil
	}

	depSet := funcs.dependencyNameSet()
	coreType := func(name string) *introspection.Type {
		t := funcs.fullSchema.Types.Get(name)
		if t == nil || !funcs.isExportableType(t) || funcs.isDependencyOwned(t, depSet) {
			return nil
		}
		return t
	}

	seen := map[string]struct{}{}
	var names []string
	add := func(name string) {
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}

	// Core object classes referenced anywhere in the surface. A value import
	// covers both `new X(ctx)` in bodies and X used as a signature type.
	for name := range funcs.collectReferencedNames(depTypes) {
		if t := coreType(name); t != nil &&
			t.Kind == introspection.TypeKindObject && len(t.Fields) > 0 {
			add(funcs.exportedTypeName(t))
		}
	}

	// Enum converters, imported only in the direction actually used (NameToValue
	// for enum return values, ValueToName for enum arguments) so the import is
	// never unused. The converter name matches its definition in client.gen.ts.
	addEnumConverters := func(ref *introspection.TypeRef, suffix string) {
		for ; ref != nil; ref = ref.OfType {
			if ref.Name == "" {
				continue
			}
			if t := coreType(ref.Name); t != nil && t.Kind == introspection.TypeKindEnum {
				add(funcs.pascalCase(t.Name) + suffix)
			}
		}
	}
	for _, t := range depTypes {
		for _, f := range t.Fields {
			addEnumConverters(f.TypeRef, "NameToValue")
			for _, a := range f.Args {
				addEnumConverters(a.TypeRef, "ValueToName")
			}
		}
	}

	sort.Strings(names)
	return names
}

// extendableClassNames returns the TS class names of the extendable core types
// (Query->Client, Binding, Env) that are actually present in the schema, in
// declaration order. Dependencies augment these classes, and client.gen.ts
// passes them to each augmentation function; gating on presence keeps the
// generated code valid against pre-Binding/Env engine schemas.
func (funcs typescriptTemplateFuncs) extendableClassNames() []string {
	if funcs.fullSchema == nil {
		return nil
	}
	var out []string
	for _, name := range introspection.ExtendableTypes {
		if funcs.fullSchema.Types.Get(name) != nil {
			out = append(out, funcs.formatName(funcs.queryToClient(name)))
		}
	}
	return out
}

// exportedTypeName returns the TS identifier under which a type is exported,
// matching the per-kind naming used by the templates: objects go through
// QueryToClient+FormatName, interfaces/inputs through FormatName, while scalars
// and enums keep their raw schema name.
func (funcs typescriptTemplateFuncs) exportedTypeName(t *introspection.Type) string {
	switch t.Kind {
	case introspection.TypeKindObject:
		return funcs.formatName(funcs.queryToClient(t.Name))
	case introspection.TypeKindInterface, introspection.TypeKindInputObject:
		return funcs.formatName(t.Name)
	default:
		return t.Name
	}
}

// exportedTypeNames collects the sorted, de-duplicated set of TS identifiers
// (formatted type names plus per-method Opts types) for the given types,
// skipping internal (_-prefixed) types, the built-in scalars, and the
// extendable types. An optional predicate further filters which types to keep.
func (funcs typescriptTemplateFuncs) exportedTypeNames(
	types []*introspection.Type,
	keep func(*introspection.Type) bool,
) []string {
	seen := map[string]struct{}{}
	var names []string

	add := func(name string) {
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}

	for _, t := range types {
		if !funcs.isExportableType(t) {
			continue
		}
		if keep != nil && !keep(t) {
			continue
		}

		add(funcs.exportedTypeName(t))

		// Per-method Opts struct types are exported alongside the object. The
		// templates name them with the raw (QueryToClient-only) type name.
		if t.Kind == introspection.TypeKindObject {
			for _, f := range t.Fields {
				if len(funcs.getOptionalArgs(f.Args)) == 0 {
					continue
				}
				add(funcs.queryToClient(t.Name) + funcs.pascalCase(f.Name) + "Opts")
			}
		}
	}

	sort.Strings(names)
	return names
}
