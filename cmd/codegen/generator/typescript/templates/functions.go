package templates

import (
	"cmp"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"text/template"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

func TypescriptTemplateFuncs(
	schemaVersion string,
	fullSchema *introspection.Schema,
	cfg generator.Config,
) template.FuncMap {
	return typescriptTemplateFuncs{
		cfg:           cfg,
		schemaVersion: schemaVersion,
		fullSchema:    fullSchema,
	}.FuncMap()
}

type typescriptTemplateFuncs struct {
	schemaVersion string
	cfg           generator.Config
	// fullSchema is the complete schema including all dependency types. It is
	// only consulted for full-schema views like the per-dep `export *` list
	// rendered in the header; per-type rendering uses the (possibly
	// filtered) schema attached to the template data.
	fullSchema *introspection.Schema
}

func (funcs typescriptTemplateFuncs) FuncMap() template.FuncMap {
	commonFunc := generator.NewCommonFunctions(funcs.schemaVersion, &FormatTypeFunc{
		formatNameFunc: funcs.formatName,
	})
	return template.FuncMap{
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
		"CheckVersionCompatibility": commonFunc.CheckVersionCompatibility,
		"ModuleRelPath":             funcs.moduleRelPath,
		"FormatProtected":           funcs.formatProtected,
		"IsClientOnly":              funcs.isClientOnly,
		"Dependencies":              funcs.Dependencies,
		"HasLocalDependencies":      funcs.HasLocalDependencies,
		"IsBundle":                  funcs.isBundle,
		"DependencyFiles":           funcs.dependencyFiles,
		"IsExtendableType":          funcs.isExtendableType,
		"DepFileName":               funcs.depFileName,
		"CoreTypeNames":             funcs.coreTypeNames,
		"DependencyExports":         funcs.dependencyExports,
		"Augmentation":              augmentation,
		"AugmentFnName":             augmentFnName,
	}
}

// AugmentationData bundles the parent class name and a dep-contributed
// field for the `augmentation_method` sub-template (text/template only
// supports one positional argument, so we wrap them).
type AugmentationData struct {
	Parent string
	Field  *introspection.Field
}

func augmentation(parent string, field *introspection.Field) AugmentationData {
	return AugmentationData{Parent: parent, Field: field}
}

// dependencyExports returns, for each dependency in the full schema, the
// kebab-cased filename and the set of TypeScript identifiers that the dep
// file exports. Used by client.gen.ts (via header.ts.gtpl) to emit named
// imports for dep-owned types it references inline (e.g. `new Hello(ctx)`
// in `Client.hello()`).
//
// `export * from "./<dep>.gen.js"` re-exports those identifiers for
// downstream consumers but does NOT bring them into client.gen.ts's
// module scope, so explicit named imports are still required.
type DependencyExport struct {
	File          string   // kebab-cased basename, no extension
	Names         []string // exported TS identifiers contributed by the dep
	AugmentFnName string   // exported function the dep file uses to attach
	// its prototype augmentations to the extendable type classes
	// (e.g. `__applyHelloAugmentations`).
}

func (funcs typescriptTemplateFuncs) dependencyExports() []DependencyExport {
	if funcs.fullSchema == nil {
		return nil
	}
	deps := funcs.fullSchema.DependencyNames()
	out := make([]DependencyExport, 0, len(deps))
	for _, dep := range deps {
		depSchema := funcs.fullSchema.Include(dep)
		seen := map[string]struct{}{}
		var names []string
		for _, t := range depSchema.Types {
			if strings.HasPrefix(t.Name, "_") {
				continue
			}
			if slices.Contains(introspection.ExtendableTypes, t.Name) {
				continue
			}
			name := funcs.queryToClient(funcs.formatName(t.Name))
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)

			// Per-method Opts struct types are also re-exported.
			if t.Kind == introspection.TypeKindObject {
				for _, f := range t.Fields {
					if len(funcs.getOptionalArgs(f.Args)) == 0 {
						continue
					}
					optsName := funcs.queryToClient(funcs.formatName(t.Name)) + funcs.pascalCase(f.Name) + "Opts"
					if _, ok := seen[optsName]; ok {
						continue
					}
					seen[optsName] = struct{}{}
					names = append(names, optsName)
				}
			}
		}
		sort.Strings(names)
		out = append(out, DependencyExport{
			File:          funcs.depFileName(dep),
			Names:         names,
			AugmentFnName: augmentFnName(dep),
		})
	}
	return out
}

// augmentFnName returns the exported function name a dep file uses to
// attach its prototype augmentations. e.g. "myDep" -> "__applyMyDepAugmentations".
func augmentFnName(depName string) string {
	return "__apply" + strcase.ToCamel(depName) + "Augmentations"
}

// coreTypeNames returns the exported TypeScript identifiers in
// client.gen.ts that a per-dep file may legally reference for *type*
// positions: every type in the full schema that is not contributed by a
// dependency. Used by _dep.ts.gtpl to emit a single
// `import type { ... } from "./client.gen.js"` line.
//
// BaseClient is intentionally NOT in this list — it must be imported as
// a *value* (for `extends BaseClient`), and importing it from
// client.gen.ts would create an ESM cycle (client.gen.ts re-exports each
// dep file). The dep template imports BaseClient from the SDK runtime
// instead.
func (funcs typescriptTemplateFuncs) coreTypeNames() []string {
	if funcs.fullSchema == nil {
		return nil
	}

	depSet := map[string]struct{}{}
	for _, d := range funcs.fullSchema.DependencyNames() {
		depSet[d] = struct{}{}
	}

	seen := map[string]struct{}{}
	var names []string

	for _, t := range funcs.fullSchema.Types {
		if strings.HasPrefix(t.Name, "_") {
			continue
		}
		switch introspection.Scalar(t.Name) {
		case introspection.ScalarString, introspection.ScalarInt,
			introspection.ScalarFloat, introspection.ScalarBoolean:
			continue
		}
		if sm := t.Directives.SourceMap(); sm != nil {
			if _, ok := depSet[sm.Module]; ok {
				continue
			}
		}
		name := funcs.queryToClient(funcs.formatName(t.Name))
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)

		// Also expose <Object>Opts types when the object has optional-argument fields.
		if t.Kind == introspection.TypeKindObject {
			for _, f := range t.Fields {
				if len(funcs.getOptionalArgs(f.Args)) == 0 {
					continue
				}
				optsName := funcs.queryToClient(funcs.formatName(t.Name)) + funcs.pascalCase(f.Name) + "Opts"
				if _, ok := seen[optsName]; ok {
					continue
				}
				seen[optsName] = struct{}{}
				names = append(names, optsName)
			}
		}
	}

	sort.Strings(names)
	return names
}

// dependencyFiles returns the per-dep generated file basenames (without
// extension) for every dependency that contributes types to the full
// schema. Used by header.ts.gtpl to emit `export *` lines.
func (funcs typescriptTemplateFuncs) dependencyFiles() []string {
	if funcs.fullSchema == nil {
		return nil
	}
	deps := funcs.fullSchema.DependencyNames()
	out := make([]string, len(deps))
	for i, d := range deps {
		out[i] = funcs.depFileName(d)
	}
	return out
}

// depFileName converts a module name to the kebab-cased file basename used
// for its generated file (e.g. "myDep" -> "my-dep").
func (funcs typescriptTemplateFuncs) depFileName(moduleName string) string {
	return strcase.ToKebab(moduleName)
}

// isExtendableType reports whether the given type is one of the core types
// (Query, Binding, Env) whose fields may be contributed by deps. Used by the
// per-dep template to skip rendering extendable types — those stay (with
// their dep-contributed fields) in client.gen.ts during Phase 1.
func (funcs typescriptTemplateFuncs) isExtendableType(t *introspection.Type) bool {
	if t == nil {
		return false
	}
	return slices.Contains(introspection.ExtendableTypes, t.Name)
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
