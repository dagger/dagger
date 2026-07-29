package templates

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"unicode"
)

// EntrypointTemplateFuncs returns the template.FuncMap used by
// src/entrypoint/*.gtpl. All inline TS expression generation lives here so
// the templates only need to handle the structural layout (loops,
// conditionals, function bodies).
func EntrypointTemplateFuncs(module *TypedefModule, opts EntrypointOptions) template.FuncMap {
	c := &entrypointFuncCtx{module: module, opts: opts}
	return template.FuncMap{
		"jsString":             jsString,
		"pascalize":            pascalize,
		"coerceExpr":           c.coerceExpr,
		"serializeExpr":        c.serializeExpr,
		"renderTypeDef":        c.renderTypeDef,
		"renderArgCall":        c.renderArgCall,
		"renderFunctionExpr":   c.renderFunctionExpr,
		"renderObjectDef":      c.renderObjectDef,
		"renderFieldCall":      c.renderFieldCall,
		"renderEnumDef":        c.renderEnumDef,
		"renderEnumMemberCall": c.renderEnumMemberCall,
		"renderInterfaceDef":   c.renderInterfaceDef,
		"classRuntimeRef":      c.classRuntimeRef,
		"classTypeRef":         c.classTypeRef,
		"isExportedClass":      c.isExportedClass,
		"coercedVarName":       coercedVarName,
		"needsTransform":       needsTransform,
		"isPrimitive":          isPrimitive,
		"isInteger":            func(t *TypedefType) bool { return t != nil && t.Kind == KindInteger },
		"argCoercionLine":      c.argCoercionLine,
		"hasDefault":           hasDefault,
		"engineIfaceTypeName":  c.engineIfaceTypeName,
		"plannedImports":       c.plannedImports,
		"isVariadic":           func(a *TypedefArgument) bool { return a.IsVariadic },
		"propFieldName":        propFieldName,
		"sortedKeysObjects":    sortedObjectKeys,
		"sortedKeysEnums":      sortedEnumKeys,
		"sortedKeysIfaces":     sortedInterfaceKeys,
		"sortedKeysMethods":    sortedFunctionKeys,
		"sortedKeysProps":      sortedPropertyKeys,
		"sortedKeysEnumValues": sortedEnumValueKeys,
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict: odd number of values")
			}
			out := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				k, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: keys must be strings")
				}
				out[k] = values[i+1]
			}
			return out, nil
		},
	}
}

type entrypointFuncCtx struct {
	module *TypedefModule
	opts   EntrypointOptions
}

func (c *entrypointFuncCtx) isExportedClass(obj *TypedefObject) bool {
	return obj.Kind == "class" && obj.IsExported
}

// entrypointReservedBindings are the module-scope identifiers the generated
// entrypoint imports from the SDK or declares itself. A user class whose name
// matches one of these must be imported under an alias, otherwise it shadows
// the entrypoint's own binding (e.g. a user `Context` class vs the SDK's
// `Context`).
var entrypointReservedBindings = map[string]bool{
	// SDK imports
	"Context":             true,
	"DaggerError":         true,
	"FunctionCachePolicy": true,
	"TypeDefKind":         true,
	"connection":          true,
	"dag":                 true,
	"getRegisteredClass":  true,
	"__dagger":            true,
	"telemetry":           true,
	// entrypoint-internal declarations
	"__loadCoreObject": true,
	"formatError":      true,
	"invoke":           true,
	"dispatch":         true,
	"register":         true,
}

// classBinding returns the local identifier an exported user class is imported
// under: the class name, unless it collides with a reserved entrypoint binding,
// in which case it's aliased.
func (c *entrypointFuncCtx) classBinding(name string) string {
	if entrypointReservedBindings[name] {
		return "__UserClass_" + name
	}
	return name
}

func (c *entrypointFuncCtx) classRuntimeRef(obj *TypedefObject) string {
	if obj.Kind == "class" && !obj.IsExported {
		return "__cls_" + obj.Name
	}
	return c.classBinding(obj.Name)
}

func (c *entrypointFuncCtx) classTypeRef(obj *TypedefObject) string {
	if obj.Kind == "class" && obj.IsExported {
		return c.classBinding(obj.Name)
	}
	return "any"
}

func (c *entrypointFuncCtx) coerceExpr(expr string, t *TypedefType) string {
	if t == nil {
		return expr
	}
	switch t.Kind {
	case KindString, KindInteger, KindFloat, KindBoolean, KindScalar, KindVoid:
		return expr
	case KindList:
		return fmt.Sprintf("(%s as any[]).map((__v) => %s)", expr, c.coerceExpr("__v", t.TypeDef))
	case KindObject:
		if _, ok := c.module.Objects[t.Name]; ok {
			return fmt.Sprintf("rebuild%s(%s)", t.Name, expr)
		}
		// Core/dependency object: the engine hands us an ID string; load a
		// typed client from it via node(id:) (unified-ID API, post-#12041).
		return fmt.Sprintf("__loadCoreObject(%s, %s)", expr, jsString(t.Name))
	case KindEnum:
		e, ok := c.module.Enums[t.Name]
		if !ok {
			return expr
		}
		entries := make([]string, 0, len(e.Values))
		for _, vName := range sortedEnumValueKeys(e.Values) {
			v := e.Values[vName]
			entries = append(entries, fmt.Sprintf("%s: %s", jsString(v.Name), jsString(v.Value)))
		}
		return fmt.Sprintf("({ %s } as Record<string, string>)[%s] ?? %s", strings.Join(entries, ", "), expr, expr)
	case KindInterface:
		if _, ok := c.module.Interfaces[t.Name]; ok {
			return fmt.Sprintf("__Iface_%s.fromID(%s)", t.Name, expr)
		}
		return expr
	default:
		return expr
	}
}

func (c *entrypointFuncCtx) serializeExpr(expr string, t *TypedefType) string {
	if t == nil {
		return expr
	}
	switch t.Kind {
	case KindString, KindInteger, KindFloat, KindBoolean, KindScalar, KindVoid:
		return expr
	case KindList:
		return fmt.Sprintf("await Promise.all((%s as any[]).map(async (__v) => %s))", expr, c.serializeExpr("__v", t.TypeDef))
	case KindObject:
		if _, ok := c.module.Objects[t.Name]; ok {
			return fmt.Sprintf("await serialize%s(%s)", t.Name, expr)
		}
		return fmt.Sprintf("await (%s).id()", expr)
	case KindEnum:
		e, ok := c.module.Enums[t.Name]
		if !ok {
			return expr
		}
		entries := make([]string, 0, len(e.Values))
		for _, vName := range sortedEnumValueKeys(e.Values) {
			v := e.Values[vName]
			entries = append(entries, fmt.Sprintf("%s: %s", jsString(v.Value), jsString(v.Name)))
		}
		return fmt.Sprintf("({ %s } as Record<string, string>)[%s] ?? %s", strings.Join(entries, ", "), expr, expr)
	case KindInterface:
		return fmt.Sprintf("await (%s).id()", expr)
	default:
		return expr
	}
}

func (c *entrypointFuncCtx) renderTypeDef(t *TypedefType) string {
	if t == nil {
		return "dag.typeDef()"
	}
	switch t.Kind {
	case KindScalar:
		return fmt.Sprintf("dag.typeDef().withScalar(%s)", jsString(t.Name))
	case KindObject:
		return fmt.Sprintf("dag.typeDef().withObject(%s)", jsString(t.Name))
	case KindList:
		return fmt.Sprintf("dag.typeDef().withListOf(%s)", c.renderTypeDef(t.TypeDef))
	case KindVoid:
		return "dag.typeDef().withKind(TypeDefKind.VoidKind).withOptional(true)"
	case KindEnum:
		return fmt.Sprintf("dag.typeDef().withEnum(%s)", jsString(t.Name))
	case KindInterface:
		return fmt.Sprintf("dag.typeDef().withInterface(%s)", jsString(t.Name))
	case KindString:
		return "dag.typeDef().withKind(TypeDefKind.StringKind)"
	case KindInteger:
		return "dag.typeDef().withKind(TypeDefKind.IntegerKind)"
	case KindFloat:
		return "dag.typeDef().withKind(TypeDefKind.FloatKind)"
	case KindBoolean:
		return "dag.typeDef().withKind(TypeDefKind.BooleanKind)"
	default:
		return "dag.typeDef()"
	}
}

func (c *entrypointFuncCtx) renderArgCall(arg *TypedefArgument) string {
	opts := map[string]string{}
	if arg.Description != "" {
		opts["description"] = jsString(arg.Description)
	}
	if arg.Deprecated != "" {
		opts["deprecated"] = jsString(arg.Deprecated)
	}
	if arg.DefaultPath != "" {
		opts["defaultPath"] = jsString(arg.DefaultPath)
	}
	if arg.DefaultAddress != "" {
		opts["defaultAddress"] = jsString(arg.DefaultAddress)
	}
	if len(arg.Ignore) > 0 {
		ignores := make([]string, len(arg.Ignore))
		for i, p := range arg.Ignore {
			ignores[i] = jsString(p)
		}
		opts["ignore"] = "[" + strings.Join(ignores, ", ") + "]"
	}
	td := c.renderTypeDef(arg.Type)
	if arg.IsOptional {
		td += ".withOptional(true)"
	}
	// An explicit `null` default must still be registered as the arg's default
	// (unlike the coercion path's hasDefault, which excludes null on purpose).
	if len(arg.DefaultValue) > 0 {
		dv, ok := c.resolveDefaultValue(arg)
		if !ok {
			if !arg.IsOptional {
				td += ".withOptional(true)"
			}
		} else {
			b, _ := json.Marshal(dv)
			opts["defaultValue"] = fmt.Sprintf("JSON.stringify(%s) as string & { __JSON: never }", string(b))
		}
	}
	if sm := sourceMapExpr(arg.Location); sm != "" {
		opts["sourceMap"] = sm
	}
	return fmt.Sprintf(".withArg(%s, %s%s)", jsString(arg.Name), td, optsLit(opts))
}

// sourceMapExpr renders a `dag.sourceMap(path, line, col)` expression for a
// location captured at codegen time, or "" when absent. The static entrypoint
// replays these baked values at runtime so no-codegen modules keep the same
// source-map comments in dependents' bindings as codegen-at-runtime modules.
func sourceMapExpr(loc *TypedefLocation) string {
	if loc == nil {
		return ""
	}
	return fmt.Sprintf("dag.sourceMap(%s, %d, %d)", jsString(loc.Filepath), loc.Line, loc.Column)
}

func (c *entrypointFuncCtx) renderObjectDef(obj *TypedefObject) string {
	opts := map[string]string{}
	if obj.Description != "" {
		opts["description"] = jsString(obj.Description)
	}
	if obj.Deprecated != "" {
		opts["deprecated"] = jsString(obj.Deprecated)
	}
	if sm := sourceMapExpr(obj.Location); sm != "" {
		opts["sourceMap"] = sm
	}
	return fmt.Sprintf("dag.typeDef().withObject(%s%s)", jsString(obj.Name), optsLit(opts))
}

func (c *entrypointFuncCtx) renderFieldCall(prop *TypedefProperty) string {
	opts := map[string]string{}
	if prop.Description != "" {
		opts["description"] = jsString(prop.Description)
	}
	if prop.Deprecated != "" {
		opts["deprecated"] = jsString(prop.Deprecated)
	}
	if sm := sourceMapExpr(prop.Location); sm != "" {
		opts["sourceMap"] = sm
	}
	return fmt.Sprintf(".withField(%s, %s%s)", jsString(propFieldName(prop)), c.renderTypeDef(prop.Type), optsLit(opts))
}

func (c *entrypointFuncCtx) renderEnumDef(e *TypedefEnum) string {
	opts := map[string]string{}
	if e.Description != "" {
		opts["description"] = jsString(e.Description)
	}
	if sm := sourceMapExpr(e.Location); sm != "" {
		opts["sourceMap"] = sm
	}
	return fmt.Sprintf("dag.typeDef().withEnum(%s%s)", jsString(e.Name), optsLit(opts))
}

func (c *entrypointFuncCtx) renderEnumMemberCall(v *TypedefEnumValue) string {
	opts := map[string]string{"value": jsString(v.Value)}
	if v.Description != "" {
		opts["description"] = jsString(v.Description)
	}
	if v.Deprecated != "" {
		opts["deprecated"] = jsString(v.Deprecated)
	}
	if sm := sourceMapExpr(v.Location); sm != "" {
		opts["sourceMap"] = sm
	}
	return fmt.Sprintf(".withEnumMember(%s%s)", jsString(v.Name), optsLit(opts))
}

func (c *entrypointFuncCtx) renderInterfaceDef(iface *TypedefInterface) string {
	opts := map[string]string{}
	if iface.Description != "" {
		opts["description"] = jsString(iface.Description)
	}
	if sm := sourceMapExpr(iface.Location); sm != "" {
		opts["sourceMap"] = sm
	}
	return fmt.Sprintf("dag.typeDef().withInterface(%s%s)", jsString(iface.Name), optsLit(opts))
}

func (c *entrypointFuncCtx) resolveDefaultValue(arg *TypedefArgument) (any, bool) {
	if !isPrimitive(arg.Type) {
		return nil, false
	}
	var v any
	if err := json.Unmarshal(arg.DefaultValue, &v); err != nil {
		return nil, false
	}
	if arg.Type.Kind != KindEnum {
		return v, true
	}
	e, ok := c.module.Enums[arg.Type.Name]
	if !ok {
		return v, true
	}
	for _, name := range sortedEnumValueKeys(e.Values) {
		val := e.Values[name]
		if val.Value == fmt.Sprintf("%v", v) {
			return val.Name, true
		}
	}
	return nil, false
}

func (c *entrypointFuncCtx) renderFunctionExpr(fn *TypedefFunction) string {
	fnName := fn.Alias
	if fnName == "" {
		fnName = fn.Name
	}
	parts := []string{fmt.Sprintf("dag.function_(%s, %s)", jsString(fnName), c.renderTypeDef(fn.ReturnType))}
	if fn.Description != "" {
		parts = append(parts, fmt.Sprintf(".withDescription(%s)", jsString(fn.Description)))
	}
	if sm := sourceMapExpr(fn.Location); sm != "" {
		parts = append(parts, fmt.Sprintf(".withSourceMap(%s)", sm))
	}
	for _, arg := range fn.Arguments {
		parts = append(parts, c.renderArgCall(arg))
	}
	switch fn.Cache {
	case "never":
		parts = append(parts, ".withCachePolicy(FunctionCachePolicy.Never)")
	case "session":
		parts = append(parts, ".withCachePolicy(FunctionCachePolicy.PerSession)")
	case "", "default":
		// nothing
	default:
		parts = append(parts, fmt.Sprintf(".withCachePolicy(FunctionCachePolicy.Default, { timeToLive: %s })", jsString(fn.Cache)))
	}
	if fn.Deprecated != "" {
		parts = append(parts, fmt.Sprintf(".withDeprecated({ reason: %s })", jsString(fn.Deprecated)))
	}
	if fn.IsCheck {
		parts = append(parts, ".withCheck()")
	}
	if fn.IsGenerator {
		parts = append(parts, ".withGenerator()")
	}
	if fn.IsUp {
		parts = append(parts, ".withUp()")
	}
	return strings.Join(parts, "")
}

// argCoercionLine emits a single `const __arg_X = ...` declaration coercing
// the engine-supplied JSON value into a runtime value the user method
// expects (node(id:) load for core IDables, rebuilder for module objects,
// enum map, iface wrap, etc.).
func (c *entrypointFuncCtx) argCoercionLine(arg *TypedefArgument) string {
	v := coercedVarName(arg)
	access := fmt.Sprintf("args[%s]", jsString(arg.Name))
	if hasDefault(arg) && isPrimitive(arg.Type) {
		return fmt.Sprintf("const %s = %s", v, c.coerceExpr(access, arg.Type))
	}
	// An omitted arg has no key in inputArgs, so `access` is undefined.
	// Normalize that (and an explicit null) to a sensible default matching the
	// runtime loader contract:
	//   - a variadic arg defaults to an empty array so the `...` spread is safe
	//   - a nullable arg with no default becomes null (user code typed
	//     `T | null` must receive null, not undefined)
	fallback := access
	switch {
	case arg.IsVariadic:
		fallback = "[]"
	case arg.IsNullable && !hasDefault(arg):
		fallback = "null"
	}
	return fmt.Sprintf(
		"const %s = %s === undefined || %s === null ? %s : %s",
		v, access, access, fallback, c.coerceExpr(access, arg.Type),
	)
}

// engineIfaceTypeName returns "<Module><Iface>" — the namespaced GraphQL type
// name under which a module interface is registered in the schema. The iface
// wrapper uses it with node(id:) (via Context.selectNode) to instantiate from
// its engine-side ID.
func (c *entrypointFuncCtx) engineIfaceTypeName(iface *TypedefInterface) string {
	return pascalize(c.module.Name) + iface.Name
}

// plannedImports returns an ordered slice of import lines to emit. Encodes
// the named/namespace/side-effect plan for the imports template.
func (c *entrypointFuncCtx) plannedImports() []importLine {
	sdk := c.opts.SDKImportPath
	if sdk == "" {
		sdk = "@dagger.io/dagger"
	}
	var lines []importLine
	lines = append(lines, importLine{
		From:  sdk,
		Names: []string{"Context", "Error as DaggerError", "FunctionCachePolicy", "TypeDefKind", "connection", "dag", "getRegisteredClass"},
	})
	// Namespace import of the generated client so __loadCoreObject can look up
	// core/dependency object classes by name when loading them from an ID.
	lines = append(lines, importLine{From: sdk, Namespace: "* as __dagger"})
	lines = append(lines, importLine{From: sdk + "/telemetry", Namespace: "* as telemetry"})

	// Group user imports by file path, deduping side-effect-only files.
	groups := map[string]*importLine{}
	order := []string{}
	for _, name := range sortedObjectKeys(c.module.Objects) {
		obj := c.module.Objects[name]
		if obj.Kind != "class" {
			continue
		}
		path, err := userImportPath(obj, c.opts)
		if err != nil {
			continue
		}
		g, ok := groups[path]
		if !ok {
			g = &importLine{From: path}
			groups[path] = g
			order = append(order, path)
		}
		switch {
		case obj.IsDefaultExport:
			// A default import binds whatever local name we choose, so just use
			// the (possibly aliased) binding directly.
			g.Default = c.classBinding(obj.Name)
		case obj.IsExported:
			if binding := c.classBinding(obj.Name); binding != obj.Name {
				g.Names = append(g.Names, obj.Name+" as "+binding)
			} else {
				g.Names = append(g.Names, obj.Name)
			}
		default:
			g.SideEffect = true
		}
	}
	for _, p := range order {
		g := groups[p]
		sort.Strings(g.Names)
		lines = append(lines, *g)
	}
	return lines
}

type importLine struct {
	From       string
	Names      []string
	Default    string // default import binding (e.g. `export default class Foo`)
	Namespace  string
	SideEffect bool
}

func userImportPath(obj *TypedefObject, opts EntrypointOptions) (string, error) {
	if obj.Location != nil && obj.Location.Filepath != "" {
		fp := obj.Location.Filepath
		var rel string
		if opts.ModuleRoot != "" && filepath.IsAbs(fp) && filepath.IsAbs(opts.ModuleRoot) {
			r, err := filepath.Rel(opts.ModuleRoot, fp)
			if err != nil {
				return "", fmt.Errorf("relative import path for %s: %w", obj.Name, err)
			}
			rel = r
		} else {
			rel = fp
		}
		for _, ext := range []string{".tsx", ".mts", ".ts"} {
			if strings.HasSuffix(rel, ext) {
				rel = strings.TrimSuffix(rel, ext)
				break
			}
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasPrefix(rel, ".") {
			rel = "./" + rel
		}
		return rel, nil
	}
	src := opts.SourceDir
	if src == "" {
		src = "src"
	}
	return "./" + src + "/index", nil
}

// ---- shared helpers -------------------------------------------------------

func needsTransform(t *TypedefType) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case KindString, KindInteger, KindFloat, KindBoolean, KindVoid:
		return false
	default:
		return true
	}
}

func isPrimitive(t *TypedefType) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case KindBoolean, KindInteger, KindString, KindFloat, KindEnum:
		return true
	}
	return false
}

func hasDefault(arg *TypedefArgument) bool {
	return len(arg.DefaultValue) > 0 && string(arg.DefaultValue) != "null"
}

func coercedVarName(arg *TypedefArgument) string {
	return "__arg_" + arg.Name
}

func propFieldName(prop *TypedefProperty) string {
	if prop.Alias != "" {
		return prop.Alias
	}
	return prop.Name
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func optsLit(opts map[string]string) string {
	if len(opts) == 0 {
		return ""
	}
	keys := sortedKeys(opts)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %s", k, opts[k]))
	}
	return ", { " + strings.Join(parts, ", ") + " }"
}

func pascalize(s string) string {
	if s == "" {
		return ""
	}
	splitOn := func(r rune) bool {
		return r == '_' || r == '-' || unicode.IsSpace(r)
	}
	segs := strings.FieldsFunc(s, splitOn)
	for i, seg := range segs {
		if seg == "" {
			continue
		}
		runes := []rune(seg)
		runes[0] = unicode.ToUpper(runes[0])
		segs[i] = string(runes)
	}
	return strings.Join(segs, "")
}

// ---- sorted helpers (deterministic output) --------------------------------

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedObjectKeys(m map[string]*TypedefObject) []string       { return sortedKeys(m) }
func sortedEnumKeys(m map[string]*TypedefEnum) []string           { return sortedKeys(m) }
func sortedEnumValueKeys(m map[string]*TypedefEnumValue) []string { return sortedKeys(m) }
func sortedFunctionKeys(m map[string]*TypedefFunction) []string   { return sortedKeys(m) }
func sortedPropertyKeys(m map[string]*TypedefProperty) []string   { return sortedKeys(m) }
func sortedInterfaceKeys(m map[string]*TypedefInterface) []string { return sortedKeys(m) }
