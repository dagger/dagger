package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/iancoleman/strcase"
	"github.com/spf13/pflag"
)

// initializeCore loads the core type definitions only
func initializeCore(ctx context.Context, dag *dagger.Client) (rdef *moduleDef, rerr error) {
	def := &moduleDef{}

	if err := def.loadTypeDefs(ctx, dag); err != nil {
		return nil, err
	}

	return def, nil
}

// initializeDefaultModule loads the module referenced by the -m,--mod flag
//
// By default, looks for a module in the current directory, or above.
// Returns an error if the module is not found or invalid.
func initializeDefaultModule(ctx context.Context, dag *dagger.Client) (*moduleDef, error) {
	modRef, _ := getExplicitModuleSourceRef()
	if modRef == "" {
		modRef = moduleURLDefault
	}
	return initializeModule(ctx, dag, modRef, true)
}

// maybeInitializeDefaultModule optionally loads the module referenced by the -m,--mod flag,
// falling back to the core definitions
func maybeInitializeDefaultModule(ctx context.Context, dag *dagger.Client) (*moduleDef, string, error) {
	modRef, _ := getExplicitModuleSourceRef()
	if modRef == "" {
		modRef = moduleURLDefault
	}
	return maybeInitializeModule(ctx, dag, modRef)
}

// initializeModule loads the module at the given source ref
//
// Returns an error if the module is not found or invalid.
func initializeModule(
	ctx context.Context,
	dag *dagger.Client,
	srcRef string,
	doFindUp bool,
	srcOpts ...dagger.ModuleSourceOpts,
) (rdef *moduleDef, rerr error) {
	ctx, span := Tracer().Start(ctx, "load module")
	defer telemetry.End(span, func() error { return rerr })

	findCtx, findSpan := Tracer().Start(ctx, "finding module configuration", telemetry.Encapsulate())
	conf, err := getModuleConfigurationForSourceRef(findCtx, dag, srcRef, doFindUp, true, srcOpts...)
	telemetry.End(findSpan, func() error { return err })

	if err != nil {
		return nil, fmt.Errorf("failed to get configured module: %w", err)
	}
	if !conf.FullyInitialized() {
		return nil, fmt.Errorf("module must be fully initialized")
	}

	return initializeModuleConfig(ctx, dag, conf)
}

// maybeInitializeModule optionally loads the module at the given source ref,
// falling back to the core definitions if the module isn't found
func maybeInitializeModule(ctx context.Context, dag *dagger.Client, srcRef string) (*moduleDef, string, error) {
	if def, err := tryInitializeModule(ctx, dag, srcRef); def != nil || err != nil {
		return def, srcRef, err
	}

	def, err := initializeCore(ctx, dag)
	return def, "", err
}

// tryInitializeModule tries to load a module if it exists
//
// Returns an error if the module is invalid or couldn't be loaded, but not
// if the module wasn't found.
func tryInitializeModule(ctx context.Context, dag *dagger.Client, srcRef string) (rdef *moduleDef, rerr error) {
	ctx, span := Tracer().Start(ctx, "looking for module")
	defer telemetry.End(span, func() error { return rerr })

	findCtx, findSpan := Tracer().Start(ctx, "finding module configuration", telemetry.Encapsulate())
	conf, _ := getModuleConfigurationForSourceRef(findCtx, dag, srcRef, true, true)
	findSpan.End()

	if conf == nil || !conf.FullyInitialized() {
		return nil, nil
	}

	span.SetName("load module " + srcRef)

	return initializeModuleConfig(ctx, dag, conf)
}

// initializeModuleConfig loads a module using a detected module configuration
func initializeModuleConfig(ctx context.Context, dag *dagger.Client, conf *configuredModule) (rdef *moduleDef, rerr error) {
	serveCtx, serveSpan := Tracer().Start(ctx, "initializing module", telemetry.Encapsulate())
	err := conf.Source.AsModule().Initialize().Serve(serveCtx)
	telemetry.End(serveSpan, func() error { return err })
	if err != nil {
		return nil, fmt.Errorf("failed to serve module: %w", err)
	}

	def, err := inspectModule(ctx, dag, conf.Source)
	if err != nil {
		return nil, err
	}

	return def, def.loadTypeDefs(ctx, dag)
}

// moduleDef is a representation of a dagger module.
type moduleDef struct {
	Name        string
	Description string
	MainObject  *modTypeDef
	Objects     []*modTypeDef
	Interfaces  []*modTypeDef
	Enums       []*modTypeDef
	Inputs      []*modTypeDef

	// the ModuleSource definition for the module, needed by some arg types
	// applying module-specific configs to the arg value.
	Source *dagger.ModuleSource

	// ModRef is the human readable module source reference as returned by the API
	ModRef string

	Dependencies []*moduleDependency
}

type moduleDependency struct {
	Name        string
	Description string
	Source      *dagger.ModuleSource

	// ModRef is the human readable module source reference as returned by the API
	ModRef string

	// RefPin is the module source pin for this dependency, if any
	RefPin string
}

func (m *moduleDependency) Short() string {
	s := m.Description
	if s == "" {
		s = "-"
	}
	return strings.SplitN(s, "\n", 2)[0]
}

//go:embed modconf.graphql
var loadModConfQuery string

//go:embed typedefs.graphql
var loadTypeDefsQuery string

func inspectModule(ctx context.Context, dag *dagger.Client, source *dagger.ModuleSource) (rdef *moduleDef, rerr error) {
	ctx, span := Tracer().Start(ctx, "inspecting module metadata", telemetry.Encapsulate())
	defer telemetry.End(span, func() error { return rerr })

	// NB: All we need most of the time is the name of the dependencies.
	// We need the descriptions when listing the dependencies, and the source
	// ref if we need to load a specific dependency. However getting the refs
	// and descriptions here, at module load, doesn't add much overhead and
	// makes it easier (and faster) later.

	var res struct {
		Source struct {
			AsString string
			Module   struct {
				Name       string
				Initialize struct {
					Description string
				}
				Dependencies []struct {
					Name        string
					Description string
					Source      struct {
						AsString string
						Pin      string
					}
				}
			}
		}
	}

	id, err := source.ID(ctx)
	if err != nil {
		return nil, err
	}

	err = dag.Do(ctx, &dagger.Request{
		Query: loadModConfQuery,
		Variables: map[string]any{
			"source": id,
		},
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return nil, fmt.Errorf("query module metadata: %w", err)
	}

	deps := make([]*moduleDependency, 0, len(res.Source.Module.Dependencies))
	for _, dep := range res.Source.Module.Dependencies {
		deps = append(deps, &moduleDependency{
			Name:        dep.Name,
			Description: dep.Description,
			ModRef:      dep.Source.AsString,
			RefPin:      dep.Source.Pin,
		})
	}

	def := &moduleDef{
		Source:       source,
		ModRef:       res.Source.AsString,
		Name:         res.Source.Module.Name,
		Description:  res.Source.Module.Initialize.Description,
		Dependencies: deps,
	}

	return def, nil
}

// loadModTypeDefs loads the objects defined by the given module in an easier to use data structure.
func (m *moduleDef) loadTypeDefs(ctx context.Context, dag *dagger.Client) (rerr error) {
	ctx, loadSpan := Tracer().Start(ctx, "loading type definitions", telemetry.Encapsulate())
	defer telemetry.End(loadSpan, func() error { return rerr })

	var res struct {
		TypeDefs []*modTypeDef
	}

	err := dag.Do(ctx, &dagger.Request{
		Query: loadTypeDefsQuery,
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return fmt.Errorf("query module objects: %w", err)
	}

	name := gqlObjectName(m.Name)
	if name == "" {
		name = "Query"
	}

	for _, typeDef := range res.TypeDefs {
		switch typeDef.Kind {
		case dagger.TypeDefKindObjectKind:
			obj := typeDef.AsObject
			// FIXME: we could get the real constructor's name through the field
			// in Query which would avoid the need to convert the module name,
			// but the Query TypeDef is loaded before the module so the module
			// isn't available in its functions list.
			if name == gqlObjectName(obj.Name) {
				m.MainObject = typeDef

				// There's always a constructor, even if the SDK didn't define one.
				// Make sure one always exists to make it easier to reuse code while
				// building out Cobra.
				if obj.Constructor == nil {
					obj.Constructor = &modFunction{ReturnType: typeDef}
				}

				if name != "Query" {
					// Constructors have an empty function name in ObjectTypeDef.
					obj.Constructor.Name = gqlFieldName(obj.Name)
				}
			}
			m.Objects = append(m.Objects, typeDef)
		case dagger.TypeDefKindInterfaceKind:
			m.Interfaces = append(m.Interfaces, typeDef)
		case dagger.TypeDefKindEnumKind:
			m.Enums = append(m.Enums, typeDef)
		case dagger.TypeDefKindInputKind:
			m.Inputs = append(m.Inputs, typeDef)
		}
	}

	if m.MainObject == nil {
		return fmt.Errorf("main object not found, check that your module's name and main object match")
	}

	m.LoadFunctionTypeDefs(m.MainObject.AsObject.Constructor)

	// FIXME: the API doesn't return the module constructor in the Query object
	rootObj := m.GetObject("Query")
	if !rootObj.HasFunction(m.MainObject.AsObject.Constructor) {
		rootObj.Functions = append(rootObj.Functions, m.MainObject.AsObject.Constructor)
	}

	return nil
}

func (m *moduleDef) Long() string {
	s := m.Name
	if m.Description != "" {
		return s + "\n\n" + m.Description
	}
	return s
}

func (m *moduleDef) AsFunctionProviders() []functionProvider {
	providers := make([]functionProvider, 0, len(m.Objects)+len(m.Interfaces))
	for _, obj := range m.AsObjects() {
		providers = append(providers, obj)
	}
	for _, iface := range m.AsInterfaces() {
		providers = append(providers, iface)
	}
	return providers
}

// AsObjects returns the module's object type definitions.
func (m *moduleDef) AsObjects() []*modObject {
	var defs []*modObject
	for _, typeDef := range m.Objects {
		if typeDef.AsObject != nil {
			defs = append(defs, typeDef.AsObject)
		}
	}
	return defs
}

func (m *moduleDef) AsInterfaces() []*modInterface {
	var defs []*modInterface
	for _, typeDef := range m.Interfaces {
		if typeDef.AsInterface != nil {
			defs = append(defs, typeDef.AsInterface)
		}
	}
	return defs
}

func (m *moduleDef) AsEnums() []*modEnum {
	var defs []*modEnum
	for _, typeDef := range m.Enums {
		if typeDef.AsEnum != nil {
			defs = append(defs, typeDef.AsEnum)
		}
	}
	return defs
}

func (m *moduleDef) AsInputs() []*modInput {
	var defs []*modInput
	for _, typeDef := range m.Inputs {
		if typeDef.AsInput != nil {
			defs = append(defs, typeDef.AsInput)
		}
	}
	return defs
}

// GetObject retrieves a saved object type definition from the module.
func (m *moduleDef) GetObject(name string) *modObject {
	for _, obj := range m.AsObjects() {
		// Normalize name in case an SDK uses a different convention for object names.
		if gqlObjectName(obj.Name) == gqlObjectName(name) {
			return obj
		}
	}
	return nil
}

func (m *moduleDef) GetObjectFunction(objectName, functionName string) (*modFunction, error) {
	fp := m.GetFunctionProvider(objectName)
	if fp == nil {
		return nil, fmt.Errorf("module %q does not have a %q object or interface", m.Name, objectName)
	}
	return m.GetFunction(fp, functionName)
}

func (m *moduleDef) GetFunction(fp functionProvider, functionName string) (*modFunction, error) {
	// This avoids an issue with module constructors overriding core functions.
	// See https://github.com/dagger/dagger/issues/9122
	if m.HasModule() && fp.ProviderName() == "Query" && m.MainObject.AsObject.Constructor.CmdName() == functionName {
		return m.MainObject.AsObject.Constructor, nil
	}
	for _, fn := range fp.GetFunctions() {
		if fn.Name == functionName || fn.CmdName() == functionName {
			m.LoadFunctionTypeDefs(fn)
			return fn, nil
		}
	}
	return nil, fmt.Errorf("no function %q in type %q", functionName, fp.ProviderName())
}

// GetInterface retrieves a saved interface type definition from the module.
func (m *moduleDef) GetInterface(name string) *modInterface {
	for _, iface := range m.AsInterfaces() {
		// Normalize name in case an SDK uses a different convention for interface names.
		if gqlObjectName(iface.Name) == gqlObjectName(name) {
			return iface
		}
	}
	return nil
}

// GetEnum retrieves a saved enum type definition from the module.
func (m *moduleDef) GetEnum(name string) *modEnum {
	for _, enum := range m.AsEnums() {
		// Normalize name in case an SDK uses a different convention for object names.
		if gqlObjectName(enum.Name) == gqlObjectName(name) {
			return enum
		}
	}
	return nil
}

// GetFunctionProvider retrieves a saved object or interface type definition from the module as a functionProvider.
func (m *moduleDef) GetFunctionProvider(name string) functionProvider {
	if obj := m.GetObject(name); obj != nil {
		return obj
	}
	if iface := m.GetInterface(name); iface != nil {
		return iface
	}
	return nil
}

// GetInput retrieves a saved input type definition from the module.
func (m *moduleDef) GetInput(name string) *modInput {
	for _, input := range m.AsInputs() {
		// Normalize name in case an SDK uses a different convention for input names.
		if gqlObjectName(input.Name) == gqlObjectName(name) {
			return input
		}
	}
	return nil
}

func (m *moduleDef) GetDependency(name string) *moduleDependency {
	for _, dep := range m.Dependencies {
		if dep.Name == name {
			return dep
		}
	}
	return nil
}

// HasModule checks if a module's definitions are loaded
func (m *moduleDef) HasModule() bool {
	return m.Name != ""
}

func (m *moduleDef) GetCoreFunctions() []*modFunction {
	all := m.GetFunctionProvider("Query").GetFunctions()
	fns := make([]*modFunction, 0, len(all))

	for _, fn := range all {
		if fn.ReturnType.AsObject != nil && !fn.ReturnType.AsObject.IsCore() || fn.Name == "" {
			continue
		}
		fns = append(fns, fn)
	}

	return fns
}

// GetCoreFunction returns a core function with the given name.
func (m *moduleDef) GetCoreFunction(name string) *modFunction {
	for _, fn := range m.GetCoreFunctions() {
		if fn.Name == name || fn.CmdName() == name {
			return fn
		}
	}
	return nil
}

// HasCoreFunction checks if there's a core function with the given name.
func (m *moduleDef) HasCoreFunction(name string) bool {
	fn := m.GetCoreFunction(name)
	return fn != nil
}

func (m *moduleDef) HasMainFunction(name string) bool {
	return m.HasFunction(m.MainObject.AsFunctionProvider(), name)
}

// HasFunction checks if an object has a function with the given name.
func (m *moduleDef) HasFunction(fp functionProvider, name string) bool {
	if fp == nil {
		return false
	}
	fn, _ := m.GetFunction(fp, name)
	return fn != nil
}

// LoadTypeDef attempts to replace a function's return object type or argument's
// object type with with one from the module's object type definitions, to
// recover missing function definitions in those places when chaining functions.
func (m *moduleDef) LoadTypeDef(typeDef *modTypeDef) {
	typeDef.mu.Lock()
	defer typeDef.mu.Unlock()

	if typeDef.AsObject != nil && typeDef.AsObject.Functions == nil && typeDef.AsObject.Fields == nil {
		obj := m.GetObject(typeDef.AsObject.Name)
		if obj != nil {
			typeDef.AsObject = obj
		}
	}
	if typeDef.AsInterface != nil && typeDef.AsInterface.Functions == nil {
		iface := m.GetInterface(typeDef.AsInterface.Name)
		if iface != nil {
			typeDef.AsInterface = iface
		}
	}
	if typeDef.AsEnum != nil {
		enum := m.GetEnum(typeDef.AsEnum.Name)
		if enum != nil {
			typeDef.AsEnum = enum
		}
	}
	if typeDef.AsInput != nil && typeDef.AsInput.Fields == nil {
		input := m.GetInput(typeDef.AsInput.Name)
		if input != nil {
			typeDef.AsInput = input
		}
	}
	if typeDef.AsList != nil {
		m.LoadTypeDef(typeDef.AsList.ElementTypeDef)
	}
}

func (m *moduleDef) LoadFunctionTypeDefs(fn *modFunction) {
	// We need to load references to types with their type definitions because
	// the introspection doesn't recursively add them, just their names.
	m.LoadTypeDef(fn.ReturnType)
	for _, arg := range fn.Args {
		m.LoadTypeDef(arg.TypeDef)
	}
}

// modTypeDef is a representation of dagger.TypeDef.
type modTypeDef struct {
	Kind        dagger.TypeDefKind
	Optional    bool
	AsObject    *modObject
	AsInterface *modInterface
	AsInput     *modInput
	AsList      *modList
	AsScalar    *modScalar
	AsEnum      *modEnum

	// mu protects concurrent update from LoadTypeDef
	mu sync.Mutex
}

func (t *modTypeDef) String() string {
	switch t.Kind {
	case dagger.TypeDefKindStringKind:
		return "string"
	case dagger.TypeDefKindIntegerKind:
		return "int"
	case dagger.TypeDefKindFloatKind:
		return "float"
	case dagger.TypeDefKindBooleanKind:
		return "bool"
	case dagger.TypeDefKindVoidKind:
		return "void"
	case dagger.TypeDefKindScalarKind:
		return t.AsScalar.Name
	case dagger.TypeDefKindEnumKind:
		return t.AsEnum.Name
	case dagger.TypeDefKindInputKind:
		return t.AsInput.Name
	case dagger.TypeDefKindObjectKind:
		return t.AsObject.Name
	case dagger.TypeDefKindInterfaceKind:
		return t.AsInterface.Name
	case dagger.TypeDefKindListKind:
		return "[]" + t.AsList.ElementTypeDef.String()
	default:
		// this should never happen because all values for kind are covered,
		// unless a new one is added and this code isn't updated
		return ""
	}
}

func (t *modTypeDef) KindDisplay() string {
	switch t.Kind {
	case dagger.TypeDefKindStringKind,
		dagger.TypeDefKindIntegerKind,
		dagger.TypeDefKindFloatKind,
		dagger.TypeDefKindBooleanKind:
		return "Scalar"
	case dagger.TypeDefKindScalarKind,
		dagger.TypeDefKindVoidKind:
		return "Custom scalar"
	case dagger.TypeDefKindEnumKind:
		return "Enum"
	case dagger.TypeDefKindInputKind:
		return "Input"
	case dagger.TypeDefKindObjectKind:
		return "Object"
	case dagger.TypeDefKindInterfaceKind:
		return "Interface"
	case dagger.TypeDefKindListKind:
		return "List of " + strings.ToLower(t.AsList.ElementTypeDef.KindDisplay()) + "s"
	default:
		return ""
	}
}

func (t *modTypeDef) Description() string {
	switch t.Kind {
	case dagger.TypeDefKindStringKind,
		dagger.TypeDefKindIntegerKind,
		dagger.TypeDefKindFloatKind,
		dagger.TypeDefKindBooleanKind:
		return "Primitive type."
	case dagger.TypeDefKindVoidKind:
		return ""
	case dagger.TypeDefKindScalarKind:
		return t.AsScalar.Description
	case dagger.TypeDefKindEnumKind:
		return t.AsEnum.Description
	case dagger.TypeDefKindInputKind:
		return t.AsInput.Description
	case dagger.TypeDefKindObjectKind:
		return t.AsObject.Description
	case dagger.TypeDefKindInterfaceKind:
		return t.AsInterface.Description
	case dagger.TypeDefKindListKind:
		return t.AsList.ElementTypeDef.Description()
	default:
		// this should never happen because all values for kind are covered,
		// unless a new one is added and this code isn't updated
		return ""
	}
}

func (t *modTypeDef) Short() string {
	s := t.String()
	if d := t.Description(); d != "" {
		return s + " - " + strings.SplitN(d, "\n", 2)[0]
	}
	return s
}

func (t *modTypeDef) Long() string {
	s := t.String()
	if d := t.Description(); d != "" {
		return s + "\n\n" + d
	}
	return s
}

type functionProvider interface {
	ProviderName() string
	GetFunctions() []*modFunction
	IsCore() bool
}

func GetSupportedFunctions(fp functionProvider) ([]*modFunction, []string) {
	allFns := fp.GetFunctions()
	fns := make([]*modFunction, 0, len(allFns))
	skipped := make([]string, 0, len(allFns))
	for _, fn := range allFns {
		if skipFunction(fp.ProviderName(), fn.Name) || fn.HasUnsupportedFlags() {
			skipped = append(skipped, fn.CmdName())
		} else {
			fns = append(fns, fn)
		}
	}
	return fns, skipped
}

func GetSupportedFunction(md *moduleDef, fp functionProvider, name string) (*modFunction, error) {
	fn, err := md.GetFunction(fp, name)
	if err != nil {
		return nil, err
	}
	_, skipped := GetSupportedFunctions(fp)
	if slices.Contains(skipped, fn.CmdName()) {
		return nil, fmt.Errorf("function %q in type %q is not supported", name, fp.ProviderName())
	}
	return fn, nil
}

func skipFunction(obj, field string) bool {
	// TODO: make this configurable in the API but may not be easy to
	// generalize because an "internal" field may still need to exist in
	// codegen, for example. Could expose if internal via the TypeDefs though.
	skip := map[string][]string{
		"Query": {
			// for SDKs only
			"builtinContainer",
			"generatedCode",
			"currentFunctionCall",
			"currentModule",
			"typeDef",
			// not useful until the CLI accepts ID inputs
			"cacheVolume",
			"setSecret",
			// for tests only
			"secret",
			// deprecated
			"pipeline",
		},
	}
	if fields, ok := skip[obj]; ok {
		return slices.Contains(fields, field)
	}
	return false
}

// skipLeaves is a map of provider names to function names that should be skipped
// when looking for leaf functions.
var skipLeaves = map[string][]string{
	"Container": {
		// imageRef should only be requested right after a `from`, and that's
		// hard to check for here.
		"imageRef",
		// stdout and stderr may be arbitrarily large and jarring to see (e.g. test suites)
		"stdout",
		"stderr",
		// avoid potential error if no previous execution
		"exitCode",
	},
	"File": {
		// This could be a binary file, so until we can tell which type of
		// file it is, best to skip it for now.
		"contents",
	},
	"Secret": {
		// Don't leak secrets.
		"plaintext",
	},
}

// GetLeafFunctions returns the leaf functions of an object or interface
//
// Leaf functions return simple values like a scalar or a list of scalars.
// If from a module, they are limited to fields. But if from a core type,
// any function without arguments is considered, excluding a few hardcoded
// ones.
//
// Functions that return an ID are excluded since the CLI can't handle them
// as input arguments yet, so they'd add noise when listing an object's leaves.
func GetLeafFunctions(fp functionProvider) []*modFunction {
	var fns []*modFunction
	// not including interfaces from modules because interfaces don't have fields
	if fp.IsCore() {
		fns = fp.GetFunctions()
	} else if obj, ok := fp.(*modObject); ok {
		fns = obj.GetFieldFunctions()
	}
	r := make([]*modFunction, 0, len(fns))

	for _, fn := range fns {
		kind := fn.ReturnType.Kind
		if kind == dagger.TypeDefKindListKind {
			kind = fn.ReturnType.AsList.ElementTypeDef.Kind
		}
		switch kind {
		case dagger.TypeDefKindObjectKind, dagger.TypeDefKindInterfaceKind, dagger.TypeDefKindVoidKind:
			continue
		case dagger.TypeDefKindScalarKind:
			// FIXME: ID types are coming from TypeDef with the wrong case ("Id")
			if fn.ReturnType.AsScalar.Name == fmt.Sprintf("%sId", fp.ProviderName()) {
				continue
			}
		}
		if names, ok := skipLeaves[fp.ProviderName()]; ok && slices.Contains(names, fn.Name) {
			continue
		}
		if fn.HasRequiredArgs() {
			continue
		}
		r = append(r, fn)
	}
	return r
}

func (t *modTypeDef) Name() string {
	if fp := t.AsFunctionProvider(); fp != nil {
		return fp.ProviderName()
	}
	return ""
}

func (t *modTypeDef) AsFunctionProvider() functionProvider {
	if t.AsList != nil {
		t = t.AsList.ElementTypeDef
	}
	if t.AsObject != nil {
		return t.AsObject
	}
	if t.AsInterface != nil {
		return t.AsInterface
	}
	return nil
}

// modObject is a representation of dagger.ObjectTypeDef.
type modObject struct {
	Name             string
	Description      string
	Functions        []*modFunction
	Fields           []*modField
	Constructor      *modFunction
	SourceModuleName string
}

var _ functionProvider = (*modObject)(nil)

func (o *modObject) ProviderName() string {
	return o.Name
}

func (o *modObject) IsCore() bool {
	return o.SourceModuleName == ""
}

// GetFunctions returns the object's function definitions including the fields,
// which are treated as functions with no arguments.
func (o *modObject) GetFunctions() []*modFunction {
	return append(o.GetFieldFunctions(), o.Functions...)
}

func (o *modObject) GetFieldFunctions() []*modFunction {
	fns := make([]*modFunction, 0, len(o.Fields))
	for _, f := range o.Fields {
		fns = append(fns, f.AsFunction())
	}
	return fns
}

func (o *modObject) HasFunction(f *modFunction) bool {
	for _, fn := range o.Functions {
		if fn.Name == f.Name {
			return true
		}
	}
	return false
}

type modInterface struct {
	Name             string
	Description      string
	Functions        []*modFunction
	SourceModuleName string
}

var _ functionProvider = (*modInterface)(nil)

func (o *modInterface) ProviderName() string {
	return o.Name
}

func (o *modInterface) IsCore() bool {
	return o.SourceModuleName == ""
}

func (o *modInterface) GetFunctions() []*modFunction {
	return o.Functions
}

type modScalar struct {
	Name        string
	Description string
}

type modEnum struct {
	Name        string
	Description string
	Values      []*modEnumValue
}

func (e *modEnum) ValueNames() []string {
	values := make([]string, 0, len(e.Values))
	for _, v := range e.Values {
		values = append(values, v.Name)
	}
	return values
}

type modEnumValue struct {
	Name        string
	Description string
}

type modInput struct {
	Name        string
	Description string
	Fields      []*modField
}

// modList is a representation of dagger.ListTypeDef.
type modList struct {
	ElementTypeDef *modTypeDef
}

// modField is a representation of dagger.FieldTypeDef.
type modField struct {
	Name        string
	Description string
	TypeDef     *modTypeDef
}

func (f *modField) AsFunction() *modFunction {
	return &modFunction{
		Name:        f.Name,
		Description: f.Description,
		ReturnType:  f.TypeDef,
	}
}

// modFunction is a representation of dagger.Function.
type modFunction struct {
	Name        string
	Description string
	ReturnType  *modTypeDef
	Args        []*modFunctionArg
	cmdName     string
	once        sync.Once
}

func (f *modFunction) CmdName() string {
	f.once.Do(func() {
		f.cmdName = cliName(f.Name)
	})
	return f.cmdName
}

func (f *modFunction) Short() string {
	s := strings.SplitN(f.Description, "\n", 2)[0]
	if s == "" {
		s = "-"
	}
	return s
}

// GetArg returns the argument definition corresponding to the given name.
func (f *modFunction) GetArg(name string) (*modFunctionArg, error) {
	for _, a := range f.Args {
		if a.FlagName() == name {
			return a, nil
		}
	}
	return nil, fmt.Errorf("no argument %q in function %q", name, f.CmdName())
}

func (f *modFunction) HasRequiredArgs() bool {
	for _, arg := range f.Args {
		if arg.IsRequired() {
			return true
		}
	}
	return false
}

func (f *modFunction) RequiredArgs() []*modFunctionArg {
	args := make([]*modFunctionArg, 0, len(f.Args))
	for _, arg := range f.Args {
		if arg.IsRequired() {
			args = append(args, arg)
		}
	}
	return args
}

func (f *modFunction) OptionalArgs() []*modFunctionArg {
	args := make([]*modFunctionArg, 0, len(f.Args))
	for _, arg := range f.Args {
		if !arg.IsRequired() {
			args = append(args, arg)
		}
	}
	return args
}

func (f *modFunction) SupportedArgs() []*modFunctionArg {
	args := make([]*modFunctionArg, 0, len(f.Args))
	for _, arg := range f.Args {
		if !arg.IsUnsupportedFlag() {
			args = append(args, arg)
		}
	}
	return args
}

func (f *modFunction) HasUnsupportedFlags() bool {
	for _, arg := range f.Args {
		if arg.IsRequired() && arg.IsUnsupportedFlag() {
			return true
		}
	}
	return false
}

func (f *modFunction) ReturnsCoreObject() bool {
	if fp := f.ReturnType.AsFunctionProvider(); fp != nil {
		return fp.IsCore()
	}
	return false
}

// modFunctionArg is a representation of dagger.FunctionArg.
type modFunctionArg struct {
	Name         string
	Description  string
	TypeDef      *modTypeDef
	DefaultValue dagger.JSON
	DefaultPath  string
	Ignore       []string
	flagName     string
	once         sync.Once
}

// FlagName returns the name of the argument using CLI naming conventions.
func (r *modFunctionArg) FlagName() string {
	r.once.Do(func() {
		r.flagName = cliName(r.Name)
	})
	return r.flagName
}

func (r *modFunctionArg) Usage() string {
	return fmt.Sprintf("--%s %s", r.FlagName(), r.TypeDef.String())
}

func (r *modFunctionArg) Short() string {
	return strings.SplitN(r.Description, "\n", 2)[0]
}

func (r *modFunctionArg) Long() string {
	sb := new(strings.Builder)
	multiline := strings.Contains(r.Description, "\n")

	if r.Description != "" {
		sb.WriteString(r.Description)
	}

	if defVal := r.defValue(); defVal != "" {
		if multiline {
			sb.WriteString("\n\n")
		} else if sb.Len() > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(fmt.Sprintf("(default: %s)", defVal))
	}

	if r.TypeDef.Kind == dagger.TypeDefKindEnumKind {
		names := strings.Join(r.TypeDef.AsEnum.ValueNames(), ", ")
		if multiline {
			sb.WriteString("\n\n")
		} else if sb.Len() > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(fmt.Sprintf("(possible values: %s)", names))
	}

	return sb.String()
}

func (r *modFunctionArg) IsRequired() bool {
	return !r.TypeDef.Optional && r.DefaultValue == ""
}

func (r *modFunctionArg) IsUnsupportedFlag() bool {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	err := r.AddFlag(flags)
	var e *UnsupportedFlagError
	return errors.As(err, &e)
}

func getDefaultValue[T any](r *modFunctionArg) (T, error) {
	var val T
	err := json.Unmarshal([]byte(r.DefaultValue), &val)
	return val, err
}

// DefValue is the default value (as text); for the usage message
func (r *modFunctionArg) defValue() string {
	if r.DefaultPath != "" {
		return fmt.Sprintf("%q", r.DefaultPath)
	}
	if r.DefaultValue == "" {
		return ""
	}
	t := r.TypeDef
	switch t.Kind {
	case dagger.TypeDefKindStringKind:
		v, err := getDefaultValue[string](r)
		if err == nil {
			return fmt.Sprintf("%q", v)
		}
	default:
		v, err := getDefaultValue[any](r)
		if err == nil {
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

// gqlObjectName converts casing to a GraphQL object  name
func gqlObjectName(name string) string {
	return strcase.ToCamel(name)
}

// gqlFieldName converts casing to a GraphQL object field name
func gqlFieldName(name string) string {
	return strcase.ToLowerCamel(name)
}

// cliName converts casing to the CLI convention (kebab)
func cliName(name string) string {
	return strcase.ToKebab(name)
}
