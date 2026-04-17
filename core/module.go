package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
)

type Module struct {
	// The source of the module
	Source dagql.Nullable[dagql.ObjectResult[*ModuleSource]] `field:"true" name:"source" doc:"The source for the module."`

	// The source to load contextual dirs/files from, which may be different than Source for blueprints or toolchains.
	ContextSource dagql.Nullable[dagql.ObjectResult[*ModuleSource]]

	// The name of the module
	NameField string `field:"true" name:"name" doc:"The name of the module"`

	// The original name of the module set in its configuration file (or first configured via withName).
	// Different than NameField when a different name was specified for the module via a dependency.
	OriginalName string

	// The module's SDKConfig, as set in the module config file
	SDKConfig *SDKConfig `field:"true" name:"sdk" doc:"The SDK config used by this module."`

	// Deps contains the module's dependency DAG.
	Deps *SchemaBuilder

	// Runtime is the container that runs the module's entrypoint. It will fail to execute if the module doesn't compile.
	Runtime dagql.Nullable[dagql.ObjectResult[*Container]]

	// The following are populated while initializing the module

	// The doc string of the module, if any
	Description string `field:"true" doc:"The doc string of the module, if any"`

	// The module's objects
	ObjectDefs dagql.ObjectResultArray[*TypeDef] `field:"true" name:"objects" doc:"Objects served by this module."`

	// The module's interfaces
	InterfaceDefs dagql.ObjectResultArray[*TypeDef] `field:"true" name:"interfaces" doc:"Interfaces served by this module."`

	// The module's enumerations
	EnumDefs dagql.ObjectResultArray[*TypeDef] `field:"true" name:"enums" doc:"Enumerations served by this module."`

	IncludeSelfInDeps bool

	// If true, disable the new default function caching behavior for this module. Functions will
	// instead default to the old behavior of per-session caching.
	DisableDefaultFunctionCaching bool

	// LegacyDefaultPath, when true, causes +defaultPath to resolve relative to
	// the workspace root instead of the module's own source directory.
	// Used for legacy blueprints/toolchains migrated to workspace modules.
	LegacyDefaultPath bool

	// Config values from workspace config.toml [modules.<name>.config].
	// Typed map: strings, bools, ints, floats as-is from TOML.
	// When set, constructor args are resolved from this map first.
	WorkspaceConfig map[string]any

	// When true and WorkspaceConfig is set, also load .env defaults
	// for args not found in WorkspaceConfig. Off by default.
	DefaultsFromDotEnv bool

	// Salts the module content cache key with internal asModule options that
	// can materially change the resulting module instance for the same source.
	AsModuleVariantDigest string
}

func (*Module) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Module",
		NonNull:   true,
	}
}

func (*Module) TypeDescription() string {
	return "A Dagger module."
}

var _ dagql.PersistedObject = (*Module)(nil)
var _ dagql.PersistedObjectDecoder = (*Module)(nil)
var _ dagql.HasDependencyResults = (*Module)(nil)

func (mod *Module) Name() string {
	return mod.NameField
}

func (mod *Module) MainObject() (*ObjectTypeDef, bool) {
	// Use OriginalName for type lookup: the SDK registers the main object
	// under the intrinsic module name (from dagger.json), which may differ
	// from NameField when a workspace config renames the module.
	name := mod.OriginalName
	if name == "" {
		name = mod.NameField
	}
	return mod.ObjectByOriginalName(name)
}

// ObjectByOriginalName finds an object by comparing against its OriginalName
// (as registered by the SDK), rather than the potentially-namespaced Name.
// This is needed because namespaceObject rewrites obj.Name to match the
// module's final name, but obj.OriginalName always reflects the SDK name.
func (mod *Module) ObjectByOriginalName(name string) (*ObjectTypeDef, bool) {
	for _, objDef := range mod.ObjectDefs {
		typeDef := objDef.Self()
		if typeDef.AsObject.Valid {
			obj := typeDef.AsObject.Value.Self()
			if gqlObjectName(obj.OriginalName) == gqlObjectName(name) {
				return obj, true
			}
		}
	}
	return nil, false
}

func (mod *Module) ObjectByName(name string) (*ObjectTypeDef, bool) {
	for _, objDef := range mod.ObjectDefs {
		typeDef := objDef.Self()
		if typeDef.AsObject.Valid {
			obj := typeDef.AsObject.Value.Self()
			if gqlObjectName(obj.Name) == gqlObjectName(name) {
				return obj, true
			}
		}
	}
	return nil, false
}

func functionRequiresArgs(fn *Function) bool {
	for _, argRes := range fn.Args {
		arg := argRes.Self()
		// NOTE: we count on user defaults already merged in the schema at this point
		// "regular optional" -> ok
		if arg.TypeDef.Self().Optional {
			continue
		}
		// "contextual optional" -> ok
		if arg.DefaultPath != "" {
			continue
		}
		// default value -> ok
		if arg.DefaultValue != nil {
			continue
		}
		return true
	}
	return false
}

func sameAttachedResult(a, b dagql.IDable) bool {
	if a == nil || b == nil {
		return false
	}
	aID, err := a.ID()
	if err != nil || aID == nil {
		return false
	}
	bID, err := b.ID()
	if err != nil || bID == nil {
		return false
	}
	return aID.EngineResultID() == bID.EngineResultID()
}

func (mod *Module) GetSource() *ModuleSource {
	if !mod.Source.Valid {
		return nil
	}
	return mod.Source.Value.Self()
}

// The "context source" is the module used as the execution context for the module.
// Usually it's simply the module source itself. But when using blueprints or
// toolchains, it will point to the downstream module applying the toolchain,
// not the toolchain itself.
func (mod *Module) GetContextSource() *ModuleSource {
	if !mod.ContextSource.Valid {
		return nil
	}
	return mod.ContextSource.Value.Self()
}

func ImplementationScopedModule(
	ctx context.Context,
	mod dagql.ObjectResult[*Module],
) (dagql.ObjectResult[*Module], error) {
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*Module]{}, fmt.Errorf("implementation-scoped module: current dagql server: %w", err)
	}

	var scoped dagql.ObjectResult[*Module]
	if err := dag.Select(ctx, mod, &scoped, dagql.Selector{Field: "_implementationScoped"}); err != nil {
		return dagql.ObjectResult[*Module]{}, fmt.Errorf("implementation-scoped module: select field: %w", err)
	}
	return scoped, nil
}

func (mod *Module) RuntimeContainer() dagql.Nullable[dagql.ObjectResult[*Container]] {
	if mod.Runtime.Valid {
		return mod.Runtime
	}
	return dagql.Nullable[dagql.ObjectResult[*Container]]{}
}

// Return all user defaults for this module
func (mod *Module) UserDefaults(ctx context.Context) (*EnvFile, error) {
	defaults := NewEnvFile(true)

	src := mod.GetSource()
	if src != nil && src.UserDefaults != nil {
		defaults = defaults.WithEnvFiles(src.UserDefaults)
	}

	return defaults, nil
}

// Return local defaults for the specified object
// An empty string as object name means the constructor.
func (mod *Module) ObjectUserDefaults(ctx context.Context, objName string) (*EnvFile, error) {
	modDefaults, err := mod.UserDefaults(ctx)
	if err != nil {
		return nil, err
	}
	isMainObject := objName == "" || strings.EqualFold(objName, strings.ReplaceAll(mod.OriginalName, "-", ""))
	if isMainObject {
		return modDefaults, nil
	}
	return modDefaults.Namespace(ctx, objName)
}

// ApplyWorkspaceDefaultsToTypeDefs updates constructor arg typedefs based on
// WorkspaceConfig, so that --help displays the correct default values.
// For primitive types (string, int, bool, float), it sets arg.DefaultValue
// to the JSON representation. For object types (Secret, Directory, etc.),
// it marks the arg as optional (since a default will be resolved at call time).
func (mod *Module) ApplyWorkspaceDefaultsToTypeDefs(ctx context.Context, dag *dagql.Server) error {
	if mod.WorkspaceConfig == nil {
		return nil
	}
	for i, objDef := range mod.ObjectDefs {
		typeDef := objDef.Self()
		if !typeDef.AsObject.Valid {
			continue
		}
		objTypeDef := typeDef.AsObject.Value
		obj := objTypeDef.Self()
		if !obj.Constructor.Valid {
			continue
		}
		updatedConstructor := obj.Constructor.Value
		for _, arg := range obj.Constructor.Value.Self().Args {
			argSelf := arg.Self()
			val, ok := lookupConfigCaseInsensitive(mod.WorkspaceConfig, argSelf.OriginalName, argSelf.Name)
			if !ok {
				continue
			}
			updatedArg := arg
			if argSelf.TypeDef.Self().Kind == TypeDefKindObject {
				if !argSelf.TypeDef.Self().Optional {
					var updatedTypeDef dagql.ObjectResult[*TypeDef]
					if err := dag.Select(ctx, argSelf.TypeDef, &updatedTypeDef, dagql.Selector{
						Field: "withOptional",
						Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(true)}},
					}); err != nil {
						return fmt.Errorf("workspace default optional arg %q type: %w", argSelf.Name, err)
					}
					if !sameAttachedResult(updatedTypeDef, argSelf.TypeDef) {
						typeDefID, err := ResultIDInput(updatedTypeDef)
						if err != nil {
							return fmt.Errorf("workspace default optional arg %q type id: %w", argSelf.Name, err)
						}
						if err := dag.Select(ctx, updatedArg, &updatedArg, dagql.Selector{
							Field: "__withTypeDef",
							Args:  []dagql.NamedInput{{Name: "typeDef", Value: typeDefID}},
						}); err != nil {
							return fmt.Errorf("workspace default optional arg %q: %w", argSelf.Name, err)
						}
					}
				}
			} else {
				userInput := configValueToString(val)
				var jsonValue JSON
				switch argSelf.TypeDef.Self().Kind {
				case TypeDefKindString:
					marshaled, err := json.Marshal(userInput)
					if err != nil {
						continue
					}
					jsonValue = JSON(marshaled)
				case TypeDefKindInteger:
					if n, err := strconv.Atoi(userInput); err == nil {
						marshaled, _ := json.Marshal(n)
						jsonValue = JSON(marshaled)
					} else {
						continue
					}
				case TypeDefKindFloat:
					if f, err := strconv.ParseFloat(userInput, 64); err == nil {
						marshaled, _ := json.Marshal(f)
						jsonValue = JSON(marshaled)
					} else {
						continue
					}
				case TypeDefKindBoolean:
					b := userInput == "true"
					marshaled, _ := json.Marshal(b)
					jsonValue = JSON(marshaled)
				default:
					if json.Valid([]byte(userInput)) {
						jsonValue = JSON(userInput)
					} else {
						marshaled, err := json.Marshal(userInput)
						if err != nil {
							continue
						}
						jsonValue = JSON(marshaled)
					}
				}
				if err := dag.Select(ctx, updatedArg, &updatedArg, dagql.Selector{
					Field: "__withDefaultValue",
					Args:  []dagql.NamedInput{{Name: "defaultValue", Value: jsonValue}},
				}); err != nil {
					return fmt.Errorf("workspace default arg %q value: %w", argSelf.Name, err)
				}
			}
			if sameAttachedResult(updatedArg, arg) {
				continue
			}
			argID, err := ResultIDInput(updatedArg)
			if err != nil {
				return fmt.Errorf("workspace default arg %q id: %w", argSelf.Name, err)
			}
			if err := dag.Select(ctx, updatedConstructor, &updatedConstructor, dagql.Selector{
				Field: "__withArg",
				Args:  []dagql.NamedInput{{Name: "arg", Value: argID}},
			}); err != nil {
				return fmt.Errorf("workspace default constructor arg %q: %w", argSelf.Name, err)
			}
		}
		if sameAttachedResult(updatedConstructor, obj.Constructor.Value) {
			continue
		}
		constructorID, err := ResultIDInput(updatedConstructor)
		if err != nil {
			return fmt.Errorf("workspace default constructor id: %w", err)
		}
		updatedObjectTypeDef := objTypeDef
		if err := dag.Select(ctx, updatedObjectTypeDef, &updatedObjectTypeDef, dagql.Selector{
			Field: "__withConstructor",
			Args:  []dagql.NamedInput{{Name: "function", Value: constructorID}},
		}); err != nil {
			return fmt.Errorf("workspace default constructor: %w", err)
		}
		if sameAttachedResult(updatedObjectTypeDef, objTypeDef) {
			continue
		}
		objectTypeDefID, err := ResultIDInput(updatedObjectTypeDef)
		if err != nil {
			return fmt.Errorf("workspace default object typedef id: %w", err)
		}
		if err := dag.Select(ctx, objDef, &mod.ObjectDefs[i], dagql.Selector{
			Field: "__withObjectTypeDef",
			Args:  []dagql.NamedInput{{Name: "objectTypeDef", Value: objectTypeDefID}},
		}); err != nil {
			return fmt.Errorf("workspace default object typedef: %w", err)
		}
	}
	return nil
}

func functionByOriginalName(obj *ObjectTypeDef, name string) (*Function, bool) {
	for _, fn := range obj.Functions {
		fnSelf := fn.Self()
		if strings.EqualFold(fnSelf.OriginalName, name) || strings.EqualFold(fnSelf.Name, gqlFieldName(name)) {
			return fnSelf, true
		}
	}
	return nil, false
}

func functionResultByOriginalName(obj *ObjectTypeDef, name string) (dagql.ObjectResult[*Function], bool) {
	for _, fn := range obj.Functions {
		fnSelf := fn.Self()
		if strings.EqualFold(fnSelf.OriginalName, name) || strings.EqualFold(fnSelf.Name, gqlFieldName(name)) {
			return fn, true
		}
	}
	return dagql.ObjectResult[*Function]{}, false
}

func lookupFunctionArg(fn *Function, name string) (*FunctionArg, bool) {
	for _, arg := range fn.Args {
		argSelf := arg.Self()
		if strings.EqualFold(argSelf.OriginalName, name) || strings.EqualFold(argSelf.Name, gqlFieldName(name)) {
			return argSelf, true
		}
	}
	return nil, false
}

func functionArgResultByName(fn *Function, name string) (dagql.ObjectResult[*FunctionArg], bool) {
	for _, arg := range fn.Args {
		argSelf := arg.Self()
		if strings.EqualFold(argSelf.OriginalName, name) || strings.EqualFold(argSelf.Name, gqlFieldName(name)) {
			return arg, true
		}
	}
	return dagql.ObjectResult[*FunctionArg]{}, false
}

func (mod *Module) objectTypeDefResultByOriginalName(name string) (dagql.ObjectResult[*TypeDef], bool) {
	for _, objDef := range mod.ObjectDefs {
		typeDef := objDef.Self()
		if typeDef.AsObject.Valid {
			obj := typeDef.AsObject.Value.Self()
			if gqlObjectName(obj.OriginalName) == gqlObjectName(name) {
				return objDef, true
			}
		}
	}
	return dagql.ObjectResult[*TypeDef]{}, false
}

func (mod *Module) objectTypeDefResultByName(name string) (dagql.ObjectResult[*TypeDef], bool) {
	for _, objDef := range mod.ObjectDefs {
		typeDef := objDef.Self()
		if typeDef.AsObject.Valid {
			obj := typeDef.AsObject.Value.Self()
			if gqlObjectName(obj.Name) == gqlObjectName(name) {
				return objDef, true
			}
		}
	}
	return dagql.ObjectResult[*TypeDef]{}, false
}

func (mod *Module) mainObjectTypeDefResult() (dagql.ObjectResult[*TypeDef], bool) {
	name := mod.OriginalName
	if name == "" {
		name = mod.NameField
	}
	return mod.objectTypeDefResultByOriginalName(name)
}

func (mod *Module) customizationTarget(path []string) (dagql.ObjectResult[*TypeDef], dagql.ObjectResult[*Function], bool) {
	objDef, ok := mod.mainObjectTypeDefResult()
	if !ok {
		return dagql.ObjectResult[*TypeDef]{}, dagql.ObjectResult[*Function]{}, false
	}
	obj := objDef.Self().AsObject.Value.Self()
	if len(path) == 0 {
		if !obj.Constructor.Valid {
			return dagql.ObjectResult[*TypeDef]{}, dagql.ObjectResult[*Function]{}, false
		}
		return objDef, obj.Constructor.Value, true
	}
	for i, segment := range path {
		fn, ok := functionResultByOriginalName(obj, segment)
		if !ok {
			return dagql.ObjectResult[*TypeDef]{}, dagql.ObjectResult[*Function]{}, false
		}
		if i == len(path)-1 {
			return objDef, fn, false
		}
		returnType := fn.Self().ReturnType.Self()
		if returnType == nil || !returnType.AsObject.Valid {
			return dagql.ObjectResult[*TypeDef]{}, dagql.ObjectResult[*Function]{}, false
		}
		nextObjDef, ok := mod.objectTypeDefResultByOriginalName(returnType.AsObject.Value.Self().OriginalName)
		if !ok {
			nextObjDef, ok = mod.objectTypeDefResultByName(returnType.AsObject.Value.Self().Name)
			if !ok {
				return dagql.ObjectResult[*TypeDef]{}, dagql.ObjectResult[*Function]{}, false
			}
		}
		objDef = nextObjDef
		obj = objDef.Self().AsObject.Value.Self()
	}
	return dagql.ObjectResult[*TypeDef]{}, dagql.ObjectResult[*Function]{}, false
}

func (mod *Module) patchFunctionArg(
	ctx context.Context,
	dag *dagql.Server,
	fn dagql.ObjectResult[*Function],
	argName string,
	patch func(dagql.ObjectResult[*FunctionArg]) (dagql.ObjectResult[*FunctionArg], error),
) (dagql.ObjectResult[*Function], bool, error) {
	arg, ok := functionArgResultByName(fn.Self(), argName)
	if !ok {
		return fn, false, nil
	}
	updatedArg, err := patch(arg)
	if err != nil {
		return fn, false, err
	}
	if sameAttachedResult(updatedArg, arg) {
		return fn, false, nil
	}
	argID, err := ResultIDInput(updatedArg)
	if err != nil {
		return fn, false, fmt.Errorf("patched arg %q id: %w", argName, err)
	}
	updatedFn := fn
	if err := dag.Select(ctx, updatedFn, &updatedFn, dagql.Selector{
		Field: "__withArg",
		Args:  []dagql.NamedInput{{Name: "arg", Value: argID}},
	}); err != nil {
		return fn, false, fmt.Errorf("patch function arg %q: %w", argName, err)
	}
	return updatedFn, !sameAttachedResult(updatedFn, fn), nil
}

func (mod *Module) ApplyLegacyCustomizationsToTypeDefs(ctx context.Context, dag *dagql.Server, customizations []*modules.ModuleConfigArgument) error {
	if len(customizations) == 0 {
		return nil
	}
	for _, cust := range customizations {
		if cust == nil {
			continue
		}
		objDef, fn, constructor := mod.customizationTarget(cust.Function)
		if !objDef.Self().AsObject.Valid {
			continue
		}
		updatedFn, changed, err := mod.patchFunctionArg(ctx, dag, fn, cust.Argument, func(arg dagql.ObjectResult[*FunctionArg]) (dagql.ObjectResult[*FunctionArg], error) {
			updatedArg := arg
			argSelf := arg.Self()
			setOptional := cust.DefaultPath != "" || cust.DefaultAddress != ""
			if setOptional && !argSelf.TypeDef.Self().Optional {
				var updatedTypeDef dagql.ObjectResult[*TypeDef]
				if err := dag.Select(ctx, argSelf.TypeDef, &updatedTypeDef, dagql.Selector{
					Field: "withOptional",
					Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(true)}},
				}); err != nil {
					return updatedArg, fmt.Errorf("legacy customization arg %q optional type: %w", argSelf.Name, err)
				}
				if !sameAttachedResult(updatedTypeDef, argSelf.TypeDef) {
					typeDefID, err := ResultIDInput(updatedTypeDef)
					if err != nil {
						return updatedArg, fmt.Errorf("legacy customization arg %q optional type id: %w", argSelf.Name, err)
					}
					if err := dag.Select(ctx, updatedArg, &updatedArg, dagql.Selector{
						Field: "__withTypeDef",
						Args:  []dagql.NamedInput{{Name: "typeDef", Value: typeDefID}},
					}); err != nil {
						return updatedArg, fmt.Errorf("legacy customization arg %q optional type apply: %w", argSelf.Name, err)
					}
				}
			}
			if jsonValue, ok := legacyArgDefaultValue(argSelf.TypeDef.Self(), cust.Default); ok {
				if err := dag.Select(ctx, updatedArg, &updatedArg, dagql.Selector{
					Field: "__withDefaultValue",
					Args:  []dagql.NamedInput{{Name: "defaultValue", Value: jsonValue}},
				}); err != nil {
					return updatedArg, fmt.Errorf("legacy customization arg %q default value: %w", argSelf.Name, err)
				}
			}
			if cust.DefaultPath != "" {
				if err := dag.Select(ctx, updatedArg, &updatedArg, dagql.Selector{
					Field: "__withDefaultPath",
					Args:  []dagql.NamedInput{{Name: "defaultPath", Value: dagql.String(cust.DefaultPath)}},
				}); err != nil {
					return updatedArg, fmt.Errorf("legacy customization arg %q default path: %w", argSelf.Name, err)
				}
				if err := dag.Select(ctx, updatedArg, &updatedArg, dagql.Selector{
					Field: "__withDefaultAddress",
					Args:  []dagql.NamedInput{{Name: "defaultAddress", Value: dagql.String("")}},
				}); err != nil {
					return updatedArg, fmt.Errorf("legacy customization arg %q clear default address: %w", argSelf.Name, err)
				}
			}
			if cust.DefaultAddress != "" {
				if err := dag.Select(ctx, updatedArg, &updatedArg, dagql.Selector{
					Field: "__withDefaultAddress",
					Args:  []dagql.NamedInput{{Name: "defaultAddress", Value: dagql.String(cust.DefaultAddress)}},
				}); err != nil {
					return updatedArg, fmt.Errorf("legacy customization arg %q default address: %w", argSelf.Name, err)
				}
				if err := dag.Select(ctx, updatedArg, &updatedArg, dagql.Selector{
					Field: "__withDefaultPath",
					Args:  []dagql.NamedInput{{Name: "defaultPath", Value: dagql.String("")}},
				}); err != nil {
					return updatedArg, fmt.Errorf("legacy customization arg %q clear default path: %w", argSelf.Name, err)
				}
			}
			if len(cust.Ignore) > 0 {
				if err := dag.Select(ctx, updatedArg, &updatedArg, dagql.Selector{
					Field: "__withIgnore",
					Args:  []dagql.NamedInput{{Name: "ignore", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(cust.Ignore...))}},
				}); err != nil {
					return updatedArg, fmt.Errorf("legacy customization arg %q ignore: %w", argSelf.Name, err)
				}
			}
			return updatedArg, nil
		})
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		updatedObjectTypeDef := objDef.Self().AsObject.Value
		fnID, err := ResultIDInput(updatedFn)
		if err != nil {
			return fmt.Errorf("legacy customization function id: %w", err)
		}
		if constructor {
			if err := dag.Select(ctx, updatedObjectTypeDef, &updatedObjectTypeDef, dagql.Selector{
				Field: "__withConstructor",
				Args:  []dagql.NamedInput{{Name: "function", Value: fnID}},
			}); err != nil {
				return fmt.Errorf("legacy customization constructor %v: %w", cust.Function, err)
			}
		} else {
			if err := dag.Select(ctx, updatedObjectTypeDef, &updatedObjectTypeDef, dagql.Selector{
				Field: "__withFunction",
				Args:  []dagql.NamedInput{{Name: "function", Value: fnID}},
			}); err != nil {
				return fmt.Errorf("legacy customization function %v: %w", cust.Function, err)
			}
		}
		objectTypeDefID, err := ResultIDInput(updatedObjectTypeDef)
		if err != nil {
			return fmt.Errorf("legacy customization object typedef id: %w", err)
		}
		for i, existing := range mod.ObjectDefs {
			if sameAttachedResult(existing, objDef) {
				if err := dag.Select(ctx, existing, &mod.ObjectDefs[i], dagql.Selector{
					Field: "__withObjectTypeDef",
					Args:  []dagql.NamedInput{{Name: "objectTypeDef", Value: objectTypeDefID}},
				}); err != nil {
					return fmt.Errorf("legacy customization object typedef: %w", err)
				}
				break
			}
		}
	}
	return nil
}

func legacyArgDefaultValue(typeDef *TypeDef, value string) (JSON, bool) {
	if value == "" {
		return nil, false
	}
	switch typeDef.Kind {
	case TypeDefKindString:
		marshaled, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		return JSON(marshaled), true
	case TypeDefKindInteger:
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, false
		}
		marshaled, err := json.Marshal(n)
		if err != nil {
			return nil, false
		}
		return JSON(marshaled), true
	case TypeDefKindFloat:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, false
		}
		marshaled, err := json.Marshal(f)
		if err != nil {
			return nil, false
		}
		return JSON(marshaled), true
	case TypeDefKindBoolean:
		b := value == "true"
		marshaled, err := json.Marshal(b)
		if err != nil {
			return nil, false
		}
		return JSON(marshaled), true
	default:
		if json.Valid([]byte(value)) {
			return JSON(value), true
		}
		marshaled, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		return JSON(marshaled), true
	}
}

func (mod *Module) Evaluate(context.Context) error {
	return nil
}

func (mod *Module) Sync(ctx context.Context) error {
	return mod.Evaluate(ctx)
}

func (mod *Module) AttachDependencyResults(
	ctx context.Context,
	self dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if mod == nil {
		return nil, nil
	}

	owned := make([]dagql.AnyResult, 0, 3+len(mod.ObjectDefs)+len(mod.InterfaceDefs)+len(mod.EnumDefs))

	if mod.Source.Valid && mod.Source.Value.Self() != nil {
		attached, err := attach(mod.Source.Value)
		if err != nil {
			return nil, fmt.Errorf("attach module source: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*ModuleSource])
		if !ok {
			return nil, fmt.Errorf("attach module source: unexpected result %T", attached)
		}
		mod.Source = dagql.NonNull(typed)
		owned = append(owned, typed)
	}
	if mod.ContextSource.Valid && mod.ContextSource.Value.Self() != nil {
		attached, err := attach(mod.ContextSource.Value)
		if err != nil {
			return nil, fmt.Errorf("attach module context source: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*ModuleSource])
		if !ok {
			return nil, fmt.Errorf("attach module context source: unexpected result %T", attached)
		}
		mod.ContextSource = dagql.NonNull(typed)
		owned = append(owned, typed)
	}
	if mod.Runtime.Valid && mod.Runtime.Value.Self() != nil {
		attached, err := attach(mod.Runtime.Value)
		if err != nil {
			return nil, fmt.Errorf("attach module runtime: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Container])
		if !ok {
			return nil, fmt.Errorf("attach module runtime: unexpected result %T", attached)
		}
		mod.Runtime = dagql.NonNull(typed)
		owned = append(owned, typed)
	}
	for i, def := range mod.ObjectDefs {
		if def.Self() == nil {
			continue
		}
		attached, err := attach(def)
		if err != nil {
			return nil, fmt.Errorf("attach module object typedef %d: %w", i, err)
		}
		typed, ok := attached.(dagql.ObjectResult[*TypeDef])
		if !ok {
			return nil, fmt.Errorf("attach module object typedef %d: unexpected result %T", i, attached)
		}
		mod.ObjectDefs[i] = typed
		owned = append(owned, typed)
	}
	for i, def := range mod.InterfaceDefs {
		if def.Self() == nil {
			continue
		}
		attached, err := attach(def)
		if err != nil {
			return nil, fmt.Errorf("attach module interface typedef %d: %w", i, err)
		}
		typed, ok := attached.(dagql.ObjectResult[*TypeDef])
		if !ok {
			return nil, fmt.Errorf("attach module interface typedef %d: unexpected result %T", i, attached)
		}
		mod.InterfaceDefs[i] = typed
		owned = append(owned, typed)
	}
	for i, def := range mod.EnumDefs {
		if def.Self() == nil {
			continue
		}
		attached, err := attach(def)
		if err != nil {
			return nil, fmt.Errorf("attach module enum typedef %d: %w", i, err)
		}
		typed, ok := attached.(dagql.ObjectResult[*TypeDef])
		if !ok {
			return nil, fmt.Errorf("attach module enum typedef %d: unexpected result %T", i, attached)
		}
		mod.EnumDefs[i] = typed
		owned = append(owned, typed)
	}

	attachModuleRef := func(child dagql.ObjectResult[*Module]) (dagql.ObjectResult[*Module], dagql.AnyResult, error) {
		if child.Self() == nil {
			return child, nil, nil
		}
		attached, err := attach(child)
		if err != nil {
			return dagql.ObjectResult[*Module]{}, nil, err
		}
		typed, ok := attached.(dagql.ObjectResult[*Module])
		if !ok {
			return dagql.ObjectResult[*Module]{}, nil, fmt.Errorf("unexpected result %T", attached)
		}
		return typed, typed, nil
	}

	if mod.Deps != nil {
		for i, dep := range mod.Deps.entries {
			depInst := dep.mod.ModuleResult()
			if depInst.Self() == nil {
				continue
			}
			attachedInst, attachedRes, err := attachModuleRef(depInst)
			if err != nil {
				return nil, fmt.Errorf("attach module dependency %q: %w", dep.mod.Name(), err)
			}
			if attachedRes == nil {
				continue
			}
			mod.Deps.entries[i].mod = NewUserMod(attachedInst)
			owned = append(owned, attachedRes)
		}
	}
	if mod.IncludeSelfInDeps {
		if mod.Deps == nil {
			return nil, fmt.Errorf("attach module self dependency: missing module deps")
		}
		attachedSelf, ok := self.(dagql.ObjectResult[*Module])
		if !ok {
			return nil, fmt.Errorf("attach module self dependency: expected attached module result, got %T", self)
		}
		attachedSelfID, err := attachedSelf.ID()
		if err != nil {
			return nil, fmt.Errorf("attach module self dependency: self ID: %w", err)
		}
		seenSelf := false
		for i, dep := range mod.Deps.entries {
			depInst := dep.mod.ModuleResult()
			if depInst.Self() == nil {
				continue
			}
			depID, err := depInst.ID()
			if err != nil {
				return nil, fmt.Errorf("attach module self dependency %q: dep ID: %w", dep.mod.Name(), err)
			}
			if depID == nil || depID.EngineResultID() != attachedSelfID.EngineResultID() {
				continue
			}
			mod.Deps.entries[i].mod = NewUserMod(attachedSelf)
			seenSelf = true
			break
		}
		if !seenSelf {
			mod.Deps = mod.Deps.Append(NewUserMod(attachedSelf))
		}
	}
	return owned, nil
}

type persistedModulePayload struct {
	SourceResultID                uint64         `json:"sourceResultID,omitempty"`
	ContextSourceResultID         uint64         `json:"contextSourceResultID,omitempty"`
	RuntimeResultID               uint64         `json:"runtimeResultID,omitempty"`
	DepModuleResultIDs            []uint64       `json:"depModuleResultIDs,omitempty"`
	IncludeSelfInDeps             bool           `json:"includeSelfInDeps,omitempty"`
	NameField                     string         `json:"nameField,omitempty"`
	OriginalName                  string         `json:"originalName,omitempty"`
	SDKConfig                     *SDKConfig     `json:"sdkConfig,omitempty"`
	Description                   string         `json:"description,omitempty"`
	ObjectDefResultIDs            []uint64       `json:"objectDefResultIDs,omitempty"`
	InterfaceDefResultIDs         []uint64       `json:"interfaceDefResultIDs,omitempty"`
	EnumDefResultIDs              []uint64       `json:"enumDefResultIDs,omitempty"`
	LegacyDefaultPath             bool           `json:"legacyDefaultPath,omitempty"`
	WorkspaceConfig               map[string]any `json:"workspaceConfig,omitempty"`
	DefaultsFromDotEnv            bool           `json:"defaultsFromDotEnv,omitempty"`
	DisableDefaultFunctionCaching bool           `json:"disableDefaultFunctionCaching,omitempty"`
}

func (mod *Module) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	var persisted persistedModulePayload
	if mod.Source.Valid {
		sourceID, err := encodePersistedObjectRef(cache, mod.Source.Value, "module source")
		if err != nil {
			return nil, err
		}
		persisted.SourceResultID = sourceID
	}
	if mod.ContextSource.Valid {
		contextSourceID, err := encodePersistedObjectRef(cache, mod.ContextSource.Value, "module context source")
		if err != nil {
			return nil, err
		}
		persisted.ContextSourceResultID = contextSourceID
	}
	if mod.Runtime.Valid {
		runtimeID, err := encodePersistedObjectRef(cache, mod.Runtime.Value, "module runtime")
		if err != nil {
			return nil, err
		}
		persisted.RuntimeResultID = runtimeID
	}

	persisted.IncludeSelfInDeps = mod.IncludeSelfInDeps
	if mod.Deps != nil {
		persisted.DepModuleResultIDs = make([]uint64, 0, len(mod.Deps.Mods()))
		for _, dep := range mod.Deps.Mods() {
			depInst := dep.ModuleResult()
			if depInst.Self() == nil {
				continue
			}
			depResultID, err := encodePersistedObjectRef(cache, depInst, fmt.Sprintf("module dependency %q", dep.Name()))
			if err != nil {
				return nil, err
			}
			if mod.IncludeSelfInDeps && depInst.Self() == mod {
				continue
			}
			persisted.DepModuleResultIDs = append(persisted.DepModuleResultIDs, depResultID)
		}
	}

	persisted.NameField = mod.NameField
	persisted.OriginalName = mod.OriginalName
	persisted.SDKConfig = mod.SDKConfig
	persisted.Description = mod.Description
	persisted.ObjectDefResultIDs = make([]uint64, 0, len(mod.ObjectDefs))
	for _, def := range mod.ObjectDefs {
		defID, err := encodePersistedObjectRef(cache, def, "module object typedef")
		if err != nil {
			return nil, err
		}
		persisted.ObjectDefResultIDs = append(persisted.ObjectDefResultIDs, defID)
	}
	persisted.InterfaceDefResultIDs = make([]uint64, 0, len(mod.InterfaceDefs))
	for _, def := range mod.InterfaceDefs {
		defID, err := encodePersistedObjectRef(cache, def, "module interface typedef")
		if err != nil {
			return nil, err
		}
		persisted.InterfaceDefResultIDs = append(persisted.InterfaceDefResultIDs, defID)
	}
	persisted.EnumDefResultIDs = make([]uint64, 0, len(mod.EnumDefs))
	for _, def := range mod.EnumDefs {
		defID, err := encodePersistedObjectRef(cache, def, "module enum typedef")
		if err != nil {
			return nil, err
		}
		persisted.EnumDefResultIDs = append(persisted.EnumDefResultIDs, defID)
	}
	persisted.LegacyDefaultPath = mod.LegacyDefaultPath
	persisted.WorkspaceConfig = mod.WorkspaceConfig
	persisted.DefaultsFromDotEnv = mod.DefaultsFromDotEnv
	persisted.DisableDefaultFunctionCaching = mod.DisableDefaultFunctionCaching

	jsonBytes, err := json.Marshal(persisted)
	if err != nil {
		return nil, fmt.Errorf("encode persisted module payload: %w", err)
	}
	return jsonBytes, nil
}

func (*Module) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedModulePayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted module payload: %w", err)
	}

	sourceRes, err := loadPersistedObjectResultByResultID[*ModuleSource](ctx, dag, persisted.SourceResultID, "module source")
	if err != nil {
		return nil, err
	}
	contextSourceRes, err := loadPersistedObjectResultByResultID[*ModuleSource](ctx, dag, persisted.ContextSourceResultID, "module context source")
	if err != nil {
		return nil, err
	}
	runtimeRes, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persisted.RuntimeResultID, "module runtime")
	if err != nil {
		return nil, err
	}

	query, err := persistedDecodeQuery(dag)
	if err != nil {
		return nil, fmt.Errorf("decode persisted module query: %w", err)
	}
	deps, err := query.DefaultDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("decode persisted module default deps: %w", err)
	}

	for _, depID := range persisted.DepModuleResultIDs {
		depRes, err := loadPersistedObjectResultByResultID[*Module](ctx, dag, depID, "module dependency")
		if err != nil {
			return nil, err
		}
		deps = deps.Append(NewUserMod(depRes))
	}

	objectDefs := make(dagql.ObjectResultArray[*TypeDef], 0, len(persisted.ObjectDefResultIDs))
	for _, defID := range persisted.ObjectDefResultIDs {
		def, err := loadPersistedObjectResultByResultID[*TypeDef](ctx, dag, defID, "module object typedef")
		if err != nil {
			return nil, err
		}
		objectDefs = append(objectDefs, def)
	}
	interfaceDefs := make(dagql.ObjectResultArray[*TypeDef], 0, len(persisted.InterfaceDefResultIDs))
	for _, defID := range persisted.InterfaceDefResultIDs {
		def, err := loadPersistedObjectResultByResultID[*TypeDef](ctx, dag, defID, "module interface typedef")
		if err != nil {
			return nil, err
		}
		interfaceDefs = append(interfaceDefs, def)
	}
	enumDefs := make(dagql.ObjectResultArray[*TypeDef], 0, len(persisted.EnumDefResultIDs))
	for _, defID := range persisted.EnumDefResultIDs {
		def, err := loadPersistedObjectResultByResultID[*TypeDef](ctx, dag, defID, "module enum typedef")
		if err != nil {
			return nil, err
		}
		enumDefs = append(enumDefs, def)
	}

	mod := &Module{
		NameField:                     persisted.NameField,
		OriginalName:                  persisted.OriginalName,
		SDKConfig:                     persisted.SDKConfig,
		Deps:                          deps,
		Description:                   persisted.Description,
		ObjectDefs:                    objectDefs,
		InterfaceDefs:                 interfaceDefs,
		EnumDefs:                      enumDefs,
		IncludeSelfInDeps:             persisted.IncludeSelfInDeps,
		LegacyDefaultPath:             persisted.LegacyDefaultPath,
		WorkspaceConfig:               persisted.WorkspaceConfig,
		DefaultsFromDotEnv:            persisted.DefaultsFromDotEnv,
		DisableDefaultFunctionCaching: persisted.DisableDefaultFunctionCaching,
	}
	if mod.SDKConfig == nil {
		mod.SDKConfig = &SDKConfig{}
	}
	if sourceRes.Self() != nil {
		mod.Source = dagql.NonNull(sourceRes)
	}
	if contextSourceRes.Self() != nil {
		mod.ContextSource = dagql.NonNull(contextSourceRes)
	}
	if runtimeRes.Self() != nil {
		mod.Runtime = dagql.NonNull(runtimeRes)
	}

	return mod, nil
}

func (mod *Module) TypeDefs(ctx context.Context, dag *dagql.Server) (dagql.ObjectResultArray[*TypeDef], error) {
	_ = ctx
	_ = dag
	typeDefs := make(dagql.ObjectResultArray[*TypeDef], 0, len(mod.ObjectDefs)+len(mod.InterfaceDefs)+len(mod.EnumDefs))
	typeDefs = append(typeDefs, mod.ObjectDefs...)
	typeDefs = append(typeDefs, mod.InterfaceDefs...)
	typeDefs = append(typeDefs, mod.EnumDefs...)
	return typeDefs, nil
}

func (mod *Module) View() (call.View, bool) {
	return "", false
}

func (mod *Module) modTypeFromDeps(ctx context.Context, typedef *TypeDef, checkDirectDeps bool) (ModType, bool, error) {
	if !checkDirectDeps {
		return nil, false, nil
	}

	// check to see if this is from a *direct* dependency
	depType, ok, err := mod.Deps.ModTypeFor(ctx, typedef)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get %s from dependency: %w", typedef.Kind, err)
	}
	return depType, ok, nil
}

func (mod *Module) modTypeForPrimitive(typedef *TypeDef) (ModType, bool) {
	return &PrimitiveType{typedef}, true
}

type moduleValidationState struct {
	validatedAttached map[uint64]struct{}
	validatedDetached map[*TypeDef]struct{}
	modTypeAttached   map[uint64]moduleValidationModTypeLookup
	modTypeDetached   map[*TypeDef]moduleValidationModTypeLookup
}

type moduleValidationModTypeLookup struct {
	modType ModType
	ok      bool
}

func (mod *Module) newValidationState() *moduleValidationState {
	return &moduleValidationState{
		validatedAttached: make(map[uint64]struct{}),
		validatedDetached: make(map[*TypeDef]struct{}),
		modTypeAttached:   make(map[uint64]moduleValidationModTypeLookup),
		modTypeDetached:   make(map[*TypeDef]moduleValidationModTypeLookup),
	}
}

func (mod *Module) validatedTypeDef(state *moduleValidationState, typeDef dagql.ObjectResult[*TypeDef]) bool {
	if state == nil || typeDef.Self() == nil {
		return false
	}
	if id, err := typeDef.ID(); err == nil && id != nil && id.EngineResultID() != 0 {
		key := id.EngineResultID()
		if _, ok := state.validatedAttached[key]; ok {
			return true
		}
		state.validatedAttached[key] = struct{}{}
		return false
	}
	if _, ok := state.validatedDetached[typeDef.Self()]; ok {
		return true
	}
	state.validatedDetached[typeDef.Self()] = struct{}{}
	return false
}

func (mod *Module) lookupValidationModType(ctx context.Context, typeDef dagql.ObjectResult[*TypeDef], state *moduleValidationState) (ModType, bool, error) {
	if state == nil {
		return mod.Deps.ModTypeFor(ctx, typeDef.Self())
	}
	if id, err := typeDef.ID(); err == nil && id != nil && id.EngineResultID() != 0 {
		key := id.EngineResultID()
		if cached, ok := state.modTypeAttached[key]; ok {
			return cached.modType, cached.ok, nil
		}
		modType, ok, err := mod.Deps.ModTypeFor(ctx, typeDef.Self())
		if err != nil {
			return nil, false, err
		}
		state.modTypeAttached[key] = moduleValidationModTypeLookup{modType: modType, ok: ok}
		return modType, ok, nil
	}
	if cached, ok := state.modTypeDetached[typeDef.Self()]; ok {
		return cached.modType, cached.ok, nil
	}
	modType, ok, err := mod.Deps.ModTypeFor(ctx, typeDef.Self())
	if err != nil {
		return nil, false, err
	}
	state.modTypeDetached[typeDef.Self()] = moduleValidationModTypeLookup{modType: modType, ok: ok}
	return modType, ok, nil
}

// verify the typedef is has no reserved names
func (mod *Module) validateTypeDef(ctx context.Context, typeDef dagql.ObjectResult[*TypeDef], state *moduleValidationState) error {
	if typeDef.Self() == nil {
		return nil
	}
	if mod.validatedTypeDef(state, typeDef) {
		return nil
	}
	switch typeDef.Self().Kind {
	case TypeDefKindList:
		return mod.validateTypeDef(ctx, typeDef.Self().AsList.Value.Self().ElementTypeDef, state)
	case TypeDefKindObject:
		return mod.validateObjectTypeDef(ctx, typeDef, state)
	case TypeDefKindInterface:
		return mod.validateInterfaceTypeDef(ctx, typeDef, state)
	}
	return nil
}

func (mod *Module) validateObjectTypeDef(ctx context.Context, typeDef dagql.ObjectResult[*TypeDef], state *moduleValidationState) error {
	// check whether this is a pre-existing object from core or another module
	modType, ok, err := mod.lookupValidationModType(ctx, typeDef, state)
	if err != nil {
		return fmt.Errorf("failed to get mod type for type def: %w", err)
	}
	if ok {
		if sourceMod := modType.SourceMod(); sourceMod != nil && sourceMod.Name() != mod.Name() {
			// already validated, skip
			return nil
		}
	}

	obj := typeDef.Self().AsObject.Value.Self()

	for _, fieldRes := range obj.Fields {
		field := fieldRes.Self()
		if gqlFieldName(field.Name) == "id" {
			return fmt.Errorf("cannot define field with reserved name %q on object %q", field.Name, obj.Name)
		}
		// Workspace cannot be stored as a field on a module object
		if field.TypeDef.Self().Kind == TypeDefKindObject && field.TypeDef.Self().AsObject.Value.Self().Name == "Workspace" {
			return fmt.Errorf("object %q field %q: Workspace cannot be stored as a field on a module object; declare it as a function argument instead",
				obj.OriginalName,
				field.OriginalName,
			)
		}
		fieldType, ok, err := mod.lookupValidationModType(ctx, field.TypeDef, state)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if ok {
			sourceMod := fieldType.SourceMod()
			// fields can reference core types and local types, but not types from other modules
			if sourceMod != nil && sourceMod.Name() != ModuleName && sourceMod.Name() != mod.Name() {
				return fmt.Errorf("object %q field %q cannot reference external type from dependency module %q",
					obj.OriginalName,
					field.OriginalName,
					sourceMod.Name(),
				)
			}
		}
		if err := mod.validateTypeDef(ctx, field.TypeDef, state); err != nil {
			return err
		}
	}

	for fn := range obj.functions() {
		if gqlFieldName(fn.Name) == "id" {
			return fmt.Errorf("cannot define function with reserved name %q on object %q", fn.Name, obj.Name)
		}
		// Check if this is a type from another (non-core) module
		retType, ok, err := mod.lookupValidationModType(ctx, fn.ReturnType, state)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if ok {
			if sourceMod := retType.SourceMod(); sourceMod != nil && sourceMod.Name() != ModuleName && sourceMod.Name() != mod.Name() {
				return fmt.Errorf("object %q function %q cannot return external type from dependency module %q",
					obj.OriginalName,
					fn.OriginalName,
					sourceMod.Name(),
				)
			}
		}
		if err := mod.validateTypeDef(ctx, fn.ReturnType, state); err != nil {
			return err
		}

		for _, argRes := range fn.Args {
			arg := argRes.Self()
			argType, ok, err := mod.lookupValidationModType(ctx, arg.TypeDef, state)
			if err != nil {
				return fmt.Errorf("failed to get mod type for type def: %w", err)
			}
			if ok {
				if sourceMod := argType.SourceMod(); sourceMod != nil && sourceMod.Name() != ModuleName && sourceMod.Name() != mod.Name() {
					return fmt.Errorf("object %q function %q arg %q cannot reference external type from dependency module %q",
						obj.OriginalName,
						fn.OriginalName,
						arg.OriginalName,
						sourceMod.Name(),
					)
				}
			}
			if err := mod.validateTypeDef(ctx, arg.TypeDef, state); err != nil {
				return err
			}
		}
	}
	return nil
}

func (mod *Module) validateInterfaceTypeDef(ctx context.Context, typeDef dagql.ObjectResult[*TypeDef], state *moduleValidationState) error {
	iface := typeDef.Self().AsInterface.Value.Self()

	// check whether this is a pre-existing interface from core or another module
	modType, ok, err := mod.lookupValidationModType(ctx, typeDef, state)
	if err != nil {
		return fmt.Errorf("failed to get mod type for type def: %w", err)
	}
	if ok {
		if sourceMod := modType.SourceMod(); sourceMod != nil && sourceMod.Name() != mod.Name() {
			// already validated, skip
			return nil
		}
	}
	for _, fnRes := range iface.Functions {
		fn := fnRes.Self()
		if gqlFieldName(fn.Name) == "id" {
			return fmt.Errorf("cannot define function with reserved name %q on interface %q", fn.Name, iface.Name)
		}
		if err := mod.validateTypeDef(ctx, fn.ReturnType, state); err != nil {
			return err
		}

		for _, argRes := range fn.Args {
			if err := mod.validateTypeDef(ctx, argRes.Self().TypeDef, state); err != nil {
				return err
			}
		}
	}
	return nil
}

// prefix the given typedef (and any recursively referenced typedefs) with this
// module's name/path for any objects
func (mod *Module) namespaceTypeDef(ctx context.Context, modPath string, typeDef dagql.ObjectResult[*TypeDef]) (dagql.ObjectResult[*TypeDef], error) {
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*TypeDef]{}, fmt.Errorf("current dagql server: %w", err)
	}

	namespaceField := func(field dagql.ObjectResult[*FieldTypeDef]) (dagql.ObjectResult[*FieldTypeDef], error) {
		updated := field
		typeDefRes, err := mod.namespaceTypeDef(ctx, modPath, field.Self().TypeDef)
		if err != nil {
			return updated, err
		}
		if !sameAttachedResult(typeDefRes, field.Self().TypeDef) {
			typeDefID, err := ResultIDInput(typeDefRes)
			if err != nil {
				return updated, fmt.Errorf("namespace field type id: %w", err)
			}
			if err := dag.Select(ctx, updated, &updated, dagql.Selector{
				Field: "__withTypeDef",
				Args:  []dagql.NamedInput{{Name: "typeDef", Value: typeDefID}},
			}); err != nil {
				return updated, fmt.Errorf("namespace field type: %w", err)
			}
		}
		sourceMap, err := mod.namespaceSourceMap(ctx, modPath, field.Self().SourceMap)
		if err != nil {
			return updated, err
		}
		if sourceMap.Valid && (!field.Self().SourceMap.Valid || !sameAttachedResult(sourceMap.Value, field.Self().SourceMap.Value)) {
			sourceMapID, err := OptionalResultIDInput(sourceMap.Value)
			if err != nil {
				return updated, fmt.Errorf("namespace field source map id: %w", err)
			}
			if err := dag.Select(ctx, updated, &updated, dagql.Selector{
				Field: "__withSourceMap",
				Args:  []dagql.NamedInput{{Name: "sourceMap", Value: sourceMapID}},
			}); err != nil {
				return updated, fmt.Errorf("namespace field source map: %w", err)
			}
		}
		return updated, nil
	}

	namespaceFunctionArg := func(arg dagql.ObjectResult[*FunctionArg]) (dagql.ObjectResult[*FunctionArg], error) {
		updated := arg
		typeDefRes, err := mod.namespaceTypeDef(ctx, modPath, arg.Self().TypeDef)
		if err != nil {
			return updated, err
		}
		if !sameAttachedResult(typeDefRes, arg.Self().TypeDef) {
			typeDefID, err := ResultIDInput(typeDefRes)
			if err != nil {
				return updated, fmt.Errorf("namespace function arg type id: %w", err)
			}
			if err := dag.Select(ctx, updated, &updated, dagql.Selector{
				Field: "__withTypeDef",
				Args:  []dagql.NamedInput{{Name: "typeDef", Value: typeDefID}},
			}); err != nil {
				return updated, fmt.Errorf("namespace function arg type: %w", err)
			}
		}
		sourceMap, err := mod.namespaceSourceMap(ctx, modPath, arg.Self().SourceMap)
		if err != nil {
			return updated, err
		}
		if sourceMap.Valid && (!arg.Self().SourceMap.Valid || !sameAttachedResult(sourceMap.Value, arg.Self().SourceMap.Value)) {
			sourceMapID, err := OptionalResultIDInput(sourceMap.Value)
			if err != nil {
				return updated, fmt.Errorf("namespace function arg source map id: %w", err)
			}
			if err := dag.Select(ctx, updated, &updated, dagql.Selector{
				Field: "__withSourceMap",
				Args:  []dagql.NamedInput{{Name: "sourceMap", Value: sourceMapID}},
			}); err != nil {
				return updated, fmt.Errorf("namespace function arg source map: %w", err)
			}
		}
		return updated, nil
	}

	namespaceFunction := func(fn dagql.ObjectResult[*Function]) (dagql.ObjectResult[*Function], error) {
		updated := fn
		returnType, err := mod.namespaceTypeDef(ctx, modPath, fn.Self().ReturnType)
		if err != nil {
			return updated, err
		}
		if !sameAttachedResult(returnType, fn.Self().ReturnType) {
			returnTypeID, err := ResultIDInput(returnType)
			if err != nil {
				return updated, fmt.Errorf("namespace function return type id: %w", err)
			}
			if err := dag.Select(ctx, updated, &updated, dagql.Selector{
				Field: "__withReturnType",
				Args:  []dagql.NamedInput{{Name: "returnType", Value: returnTypeID}},
			}); err != nil {
				return updated, fmt.Errorf("namespace function return type: %w", err)
			}
		}
		sourceMap, err := mod.namespaceSourceMap(ctx, modPath, fn.Self().SourceMap)
		if err != nil {
			return updated, err
		}
		if sourceMap.Valid && (!fn.Self().SourceMap.Valid || !sameAttachedResult(sourceMap.Value, fn.Self().SourceMap.Value)) {
			sourceMapID, err := OptionalResultIDInput(sourceMap.Value)
			if err != nil {
				return updated, fmt.Errorf("namespace function source map id: %w", err)
			}
			if err := dag.Select(ctx, updated, &updated, dagql.Selector{
				Field: "__withSourceMap",
				Args:  []dagql.NamedInput{{Name: "sourceMap", Value: sourceMapID}},
			}); err != nil {
				return updated, fmt.Errorf("namespace function source map: %w", err)
			}
		}
		for _, arg := range fn.Self().Args {
			updatedArg, err := namespaceFunctionArg(arg)
			if err != nil {
				return updated, err
			}
			if !sameAttachedResult(updatedArg, arg) {
				argID, err := ResultIDInput(updatedArg)
				if err != nil {
					return updated, fmt.Errorf("namespace function arg id: %w", err)
				}
				if err := dag.Select(ctx, updated, &updated, dagql.Selector{
					Field: "__withArg",
					Args:  []dagql.NamedInput{{Name: "arg", Value: argID}},
				}); err != nil {
					return updated, fmt.Errorf("namespace function arg: %w", err)
				}
			}
		}
		return updated, nil
	}

	switch typeDef.Self().Kind {
	case TypeDefKindList:
		list := typeDef.Self().AsList.Value
		elementTypeDef, err := mod.namespaceTypeDef(ctx, modPath, list.Self().ElementTypeDef)
		if err != nil {
			return typeDef, err
		}
		if sameAttachedResult(elementTypeDef, list.Self().ElementTypeDef) {
			return typeDef, nil
		}
		elementTypeDefID, err := ResultIDInput(elementTypeDef)
		if err != nil {
			return typeDef, fmt.Errorf("namespace list element type id: %w", err)
		}
		var updatedList dagql.ObjectResult[*ListTypeDef]
		if err := dag.Select(ctx, list, &updatedList, dagql.Selector{
			Field: "__withElementTypeDef",
			Args:  []dagql.NamedInput{{Name: "elementTypeDef", Value: elementTypeDefID}},
		}); err != nil {
			return typeDef, fmt.Errorf("namespace list element type: %w", err)
		}
		updatedListID, err := ResultIDInput(updatedList)
		if err != nil {
			return typeDef, fmt.Errorf("namespace list typedef id: %w", err)
		}
		updated := typeDef
		if err := dag.Select(ctx, updated, &updated, dagql.Selector{
			Field: "__withListTypeDef",
			Args:  []dagql.NamedInput{{Name: "listTypeDef", Value: updatedListID}},
		}); err != nil {
			return typeDef, fmt.Errorf("namespace list typedef: %w", err)
		}
		return updated, nil
	case TypeDefKindObject:
		obj := typeDef.Self().AsObject.Value
		updatedObj := obj
		_, ok, err := mod.Deps.ModTypeFor(ctx, typeDef.Self())
		if err != nil {
			return typeDef, fmt.Errorf("namespace object type lookup: %w", err)
		}
		if !ok {
			targetName := namespaceObject(obj.Self().OriginalName, mod.Name(), mod.OriginalName)
			if obj.Self().Name != targetName {
				if err := dag.Select(ctx, updatedObj, &updatedObj, dagql.Selector{
					Field: "__withName",
					Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(targetName)}},
				}); err != nil {
					return typeDef, fmt.Errorf("namespace object name: %w", err)
				}
			}
			if obj.Self().SourceModuleName != mod.Name() {
				if err := dag.Select(ctx, updatedObj, &updatedObj, dagql.Selector{
					Field: "__withSourceModuleName",
					Args:  []dagql.NamedInput{{Name: "sourceModuleName", Value: OptSourceModuleName(mod.Name())}},
				}); err != nil {
					return typeDef, fmt.Errorf("namespace object source module name: %w", err)
				}
			}
			sourceMap, err := mod.namespaceSourceMap(ctx, modPath, obj.Self().SourceMap)
			if err != nil {
				return typeDef, err
			}
			if sourceMap.Valid && (!obj.Self().SourceMap.Valid || !sameAttachedResult(sourceMap.Value, obj.Self().SourceMap.Value)) {
				sourceMapID, err := OptionalResultIDInput(sourceMap.Value)
				if err != nil {
					return typeDef, fmt.Errorf("namespace object source map id: %w", err)
				}
				if err := dag.Select(ctx, updatedObj, &updatedObj, dagql.Selector{
					Field: "__withSourceMap",
					Args:  []dagql.NamedInput{{Name: "sourceMap", Value: sourceMapID}},
				}); err != nil {
					return typeDef, fmt.Errorf("namespace object source map: %w", err)
				}
			}
		}
		for _, field := range obj.Self().Fields {
			updatedField, err := namespaceField(field)
			if err != nil {
				return typeDef, err
			}
			if !sameAttachedResult(updatedField, field) {
				fieldID, err := ResultIDInput(updatedField)
				if err != nil {
					return typeDef, fmt.Errorf("namespace object field id: %w", err)
				}
				if err := dag.Select(ctx, updatedObj, &updatedObj, dagql.Selector{
					Field: "__withField",
					Args:  []dagql.NamedInput{{Name: "field", Value: fieldID}},
				}); err != nil {
					return typeDef, fmt.Errorf("namespace object field: %w", err)
				}
			}
		}
		for _, fn := range obj.Self().Functions {
			updatedFn, err := namespaceFunction(fn)
			if err != nil {
				return typeDef, err
			}
			if !sameAttachedResult(updatedFn, fn) {
				fnID, err := ResultIDInput(updatedFn)
				if err != nil {
					return typeDef, fmt.Errorf("namespace object function id: %w", err)
				}
				if err := dag.Select(ctx, updatedObj, &updatedObj, dagql.Selector{
					Field: "__withFunction",
					Args:  []dagql.NamedInput{{Name: "function", Value: fnID}},
				}); err != nil {
					return typeDef, fmt.Errorf("namespace object function: %w", err)
				}
			}
		}
		if obj.Self().Constructor.Valid {
			updatedConstructor, err := namespaceFunction(obj.Self().Constructor.Value)
			if err != nil {
				return typeDef, err
			}
			if !sameAttachedResult(updatedConstructor, obj.Self().Constructor.Value) {
				constructorID, err := ResultIDInput(updatedConstructor)
				if err != nil {
					return typeDef, fmt.Errorf("namespace constructor id: %w", err)
				}
				if err := dag.Select(ctx, updatedObj, &updatedObj, dagql.Selector{
					Field: "__withConstructor",
					Args:  []dagql.NamedInput{{Name: "function", Value: constructorID}},
				}); err != nil {
					return typeDef, fmt.Errorf("namespace constructor: %w", err)
				}
			}
		}
		if sameAttachedResult(updatedObj, obj) {
			return typeDef, nil
		}
		updatedObjID, err := ResultIDInput(updatedObj)
		if err != nil {
			return typeDef, fmt.Errorf("namespace object typedef id: %w", err)
		}
		updated := typeDef
		if err := dag.Select(ctx, updated, &updated, dagql.Selector{
			Field: "__withObjectTypeDef",
			Args:  []dagql.NamedInput{{Name: "objectTypeDef", Value: updatedObjID}},
		}); err != nil {
			return typeDef, fmt.Errorf("namespace object typedef: %w", err)
		}
		return updated, nil
	case TypeDefKindInterface:
		iface := typeDef.Self().AsInterface.Value
		updatedIface := iface
		_, ok, err := mod.Deps.ModTypeFor(ctx, typeDef.Self())
		if err != nil {
			return typeDef, fmt.Errorf("namespace interface type lookup: %w", err)
		}
		if !ok {
			targetName := namespaceObject(iface.Self().OriginalName, mod.Name(), mod.OriginalName)
			if iface.Self().Name != targetName {
				if err := dag.Select(ctx, updatedIface, &updatedIface, dagql.Selector{
					Field: "__withName",
					Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(targetName)}},
				}); err != nil {
					return typeDef, fmt.Errorf("namespace interface name: %w", err)
				}
			}
			if iface.Self().SourceModuleName != mod.Name() {
				if err := dag.Select(ctx, updatedIface, &updatedIface, dagql.Selector{
					Field: "__withSourceModuleName",
					Args:  []dagql.NamedInput{{Name: "sourceModuleName", Value: OptSourceModuleName(mod.Name())}},
				}); err != nil {
					return typeDef, fmt.Errorf("namespace interface source module name: %w", err)
				}
			}
			sourceMap, err := mod.namespaceSourceMap(ctx, modPath, iface.Self().SourceMap)
			if err != nil {
				return typeDef, err
			}
			if sourceMap.Valid && (!iface.Self().SourceMap.Valid || !sameAttachedResult(sourceMap.Value, iface.Self().SourceMap.Value)) {
				sourceMapID, err := OptionalResultIDInput(sourceMap.Value)
				if err != nil {
					return typeDef, fmt.Errorf("namespace interface source map id: %w", err)
				}
				if err := dag.Select(ctx, updatedIface, &updatedIface, dagql.Selector{
					Field: "__withSourceMap",
					Args:  []dagql.NamedInput{{Name: "sourceMap", Value: sourceMapID}},
				}); err != nil {
					return typeDef, fmt.Errorf("namespace interface source map: %w", err)
				}
			}
		}
		for _, fn := range iface.Self().Functions {
			updatedFn, err := namespaceFunction(fn)
			if err != nil {
				return typeDef, err
			}
			if !sameAttachedResult(updatedFn, fn) {
				fnID, err := ResultIDInput(updatedFn)
				if err != nil {
					return typeDef, fmt.Errorf("namespace interface function id: %w", err)
				}
				if err := dag.Select(ctx, updatedIface, &updatedIface, dagql.Selector{
					Field: "__withFunction",
					Args:  []dagql.NamedInput{{Name: "function", Value: fnID}},
				}); err != nil {
					return typeDef, fmt.Errorf("namespace interface function: %w", err)
				}
			}
		}
		if sameAttachedResult(updatedIface, iface) {
			return typeDef, nil
		}
		updatedIfaceID, err := ResultIDInput(updatedIface)
		if err != nil {
			return typeDef, fmt.Errorf("namespace interface typedef id: %w", err)
		}
		updated := typeDef
		if err := dag.Select(ctx, updated, &updated, dagql.Selector{
			Field: "__withInterfaceTypeDef",
			Args:  []dagql.NamedInput{{Name: "interfaceTypeDef", Value: updatedIfaceID}},
		}); err != nil {
			return typeDef, fmt.Errorf("namespace interface typedef: %w", err)
		}
		return updated, nil
	case TypeDefKindEnum:
		enum := typeDef.Self().AsEnum.Value
		updatedEnum := enum
		_, ok, err := mod.Deps.ModTypeFor(ctx, typeDef.Self())
		if err != nil {
			return typeDef, fmt.Errorf("namespace enum type lookup: %w", err)
		}
		if ok {
			return typeDef, nil
		}
		if !ok {
			targetName := namespaceObject(enum.Self().OriginalName, mod.Name(), mod.OriginalName)
			if enum.Self().Name != targetName {
				if err := dag.Select(ctx, updatedEnum, &updatedEnum, dagql.Selector{
					Field: "__withName",
					Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(targetName)}},
				}); err != nil {
					return typeDef, fmt.Errorf("namespace enum name: %w", err)
				}
			}
			if enum.Self().SourceModuleName != mod.Name() {
				if err := dag.Select(ctx, updatedEnum, &updatedEnum, dagql.Selector{
					Field: "__withSourceModuleName",
					Args:  []dagql.NamedInput{{Name: "sourceModuleName", Value: OptSourceModuleName(mod.Name())}},
				}); err != nil {
					return typeDef, fmt.Errorf("namespace enum source module name: %w", err)
				}
			}
			sourceMap, err := mod.namespaceSourceMap(ctx, modPath, enum.Self().SourceMap)
			if err != nil {
				return typeDef, err
			}
			if sourceMap.Valid && (!enum.Self().SourceMap.Valid || !sameAttachedResult(sourceMap.Value, enum.Self().SourceMap.Value)) {
				sourceMapID, err := OptionalResultIDInput(sourceMap.Value)
				if err != nil {
					return typeDef, fmt.Errorf("namespace enum source map id: %w", err)
				}
				if err := dag.Select(ctx, updatedEnum, &updatedEnum, dagql.Selector{
					Field: "__withSourceMap",
					Args:  []dagql.NamedInput{{Name: "sourceMap", Value: sourceMapID}},
				}); err != nil {
					return typeDef, fmt.Errorf("namespace enum source map: %w", err)
				}
			}
		}
		for _, member := range enum.Self().Members {
			sourceMap, err := mod.namespaceSourceMap(ctx, modPath, member.Self().SourceMap)
			if err != nil {
				return typeDef, err
			}
			if sourceMap.Valid && (!member.Self().SourceMap.Valid || !sameAttachedResult(sourceMap.Value, member.Self().SourceMap.Value)) {
				sourceMapID, err := OptionalResultIDInput(sourceMap.Value)
				if err != nil {
					return typeDef, fmt.Errorf("namespace enum member source map id: %w", err)
				}
				var updatedMember dagql.ObjectResult[*EnumMemberTypeDef]
				if err := dag.Select(ctx, member, &updatedMember, dagql.Selector{
					Field: "__withSourceMap",
					Args:  []dagql.NamedInput{{Name: "sourceMap", Value: sourceMapID}},
				}); err != nil {
					return typeDef, fmt.Errorf("namespace enum member source map: %w", err)
				}
				memberID, err := ResultIDInput(updatedMember)
				if err != nil {
					return typeDef, fmt.Errorf("namespace enum member id: %w", err)
				}
				if err := dag.Select(ctx, updatedEnum, &updatedEnum, dagql.Selector{
					Field: "__withMember",
					Args:  []dagql.NamedInput{{Name: "member", Value: memberID}},
				}); err != nil {
					return typeDef, fmt.Errorf("namespace enum member: %w", err)
				}
			}
		}
		if sameAttachedResult(updatedEnum, enum) {
			return typeDef, nil
		}
		updatedEnumID, err := ResultIDInput(updatedEnum)
		if err != nil {
			return typeDef, fmt.Errorf("namespace enum typedef id: %w", err)
		}
		updated := typeDef
		if err := dag.Select(ctx, updated, &updated, dagql.Selector{
			Field: "__withEnumTypeDef",
			Args:  []dagql.NamedInput{{Name: "enumTypeDef", Value: updatedEnumID}},
		}); err != nil {
			return typeDef, fmt.Errorf("namespace enum typedef: %w", err)
		}
		return updated, nil
	default:
		return typeDef, nil
	}
}

func (mod *Module) namespaceSourceMap(
	ctx context.Context,
	modPath string,
	sourceMap dagql.Nullable[dagql.ObjectResult[*SourceMap]],
) (dagql.Nullable[dagql.ObjectResult[*SourceMap]], error) {
	if !sourceMap.Valid || sourceMap.Value.Self() == nil {
		return sourceMap, nil
	}
	filename := filepath.Join(modPath, sourceMap.Value.Self().Filename)
	url := sourceMap.Value.Self().URL
	if mod.Source.Valid && mod.Source.Value.Self().Kind == ModuleSourceKindGit {
		link, err := mod.Source.Value.Self().Git.Link(filename, sourceMap.Value.Self().Line, sourceMap.Value.Self().Column)
		if err != nil {
			return dagql.Nullable[dagql.ObjectResult[*SourceMap]]{}, nil
		}
		url = link
	}
	if sourceMap.Value.Self().Module == mod.Name() &&
		sourceMap.Value.Self().Filename == filename &&
		sourceMap.Value.Self().URL == url {
		return sourceMap, nil
	}
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.Nullable[dagql.ObjectResult[*SourceMap]]{}, fmt.Errorf("current dagql server: %w", err)
	}
	var updated dagql.ObjectResult[*SourceMap]
	args := []dagql.NamedInput{
		{Name: "module", Value: dagql.Opt(dagql.String(mod.Name()))},
		{Name: "filename", Value: dagql.String(filename)},
		{Name: "line", Value: dagql.Int(sourceMap.Value.Self().Line)},
		{Name: "column", Value: dagql.Int(sourceMap.Value.Self().Column)},
	}
	if url != "" {
		args = append(args, dagql.NamedInput{Name: "url", Value: dagql.Opt(dagql.String(url))})
	}
	if err := dag.Select(ctx, dag.Root(), &updated, dagql.Selector{
		Field: "sourceMap",
		Args:  args,
	}); err != nil {
		return dagql.Nullable[dagql.ObjectResult[*SourceMap]]{}, fmt.Errorf("namespace source map: %w", err)
	}
	return dagql.NonNull(updated), nil
}

// modulePath gets the prefix for the file sourcemaps, so that the sourcemap is
// relative to the context directory
func (mod *Module) modulePath() string {
	return mod.Source.Value.Self().SourceSubpath
}

// Patch is called after all types have been loaded - here we can update any
// definitions as required, and attempt to resolve references.
func (mod *Module) Patch(ctx context.Context) error {
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return fmt.Errorf("current dagql server: %w", err)
	}

	patchFunctionEnumDefaults := func(fn dagql.ObjectResult[*Function]) (dagql.ObjectResult[*Function], error) {
		updated := fn
		for _, arg := range fn.Self().Args {
			argSelf := arg.Self()
			if argSelf.DefaultValue == nil {
				continue
			}
			if argSelf.TypeDef.Self().Kind != TypeDefKindEnum {
				continue
			}
			var enumTypeDef *EnumTypeDef
			for _, enum := range mod.EnumDefs {
				if enum.Self().AsEnum.Value.Self().Name == argSelf.TypeDef.Self().AsEnum.Value.Self().Name {
					enumTypeDef = enum.Self().AsEnum.Value.Self()
					break
				}
			}
			if enumTypeDef == nil {
				continue
			}

			var val string
			dec := json.NewDecoder(bytes.NewReader(argSelf.DefaultValue.Bytes()))
			dec.UseNumber()
			if err := dec.Decode(&val); err != nil {
				return updated, fmt.Errorf("failed to decode default value for arg %q: %w", argSelf.Name, err)
			}

			found := false
			for _, member := range enumTypeDef.Members {
				memberSelf := member.Self()
				if val == memberSelf.OriginalName {
					val = memberSelf.Name
					found = true
					break
				}
			}
			if !found {
				return updated, fmt.Errorf("enum name %q not found", val)
			}

			res, err := json.Marshal(val)
			if err != nil {
				return updated, err
			}
			var updatedArg dagql.ObjectResult[*FunctionArg]
			if err := dag.Select(ctx, arg, &updatedArg, dagql.Selector{
				Field: "__withDefaultValue",
				Args:  []dagql.NamedInput{{Name: "defaultValue", Value: JSON(res)}},
			}); err != nil {
				return updated, fmt.Errorf("patch enum default arg %q: %w", argSelf.Name, err)
			}
			if sameAttachedResult(updatedArg, arg) {
				continue
			}
			argID, err := ResultIDInput(updatedArg)
			if err != nil {
				return updated, fmt.Errorf("patched enum default arg %q id: %w", argSelf.Name, err)
			}
			if err := dag.Select(ctx, updated, &updated, dagql.Selector{
				Field: "__withArg",
				Args:  []dagql.NamedInput{{Name: "arg", Value: argID}},
			}); err != nil {
				return updated, fmt.Errorf("patch enum default function arg %q: %w", argSelf.Name, err)
			}
		}
		return updated, nil
	}

	for i, obj := range mod.ObjectDefs {
		objRes := obj.Self().AsObject.Value
		updatedObj := objRes
		for _, fn := range objRes.Self().Functions {
			updatedFn, err := patchFunctionEnumDefaults(fn)
			if err != nil {
				return err
			}
			if sameAttachedResult(updatedFn, fn) {
				continue
			}
			fnID, err := ResultIDInput(updatedFn)
			if err != nil {
				return fmt.Errorf("patched object function id: %w", err)
			}
			if err := dag.Select(ctx, updatedObj, &updatedObj, dagql.Selector{
				Field: "__withFunction",
				Args:  []dagql.NamedInput{{Name: "function", Value: fnID}},
			}); err != nil {
				return fmt.Errorf("patch object function enum defaults: %w", err)
			}
		}
		if objRes.Self().Constructor.Valid {
			updatedFn, err := patchFunctionEnumDefaults(objRes.Self().Constructor.Value)
			if err != nil {
				return err
			}
			if !sameAttachedResult(updatedFn, objRes.Self().Constructor.Value) {
				fnID, err := ResultIDInput(updatedFn)
				if err != nil {
					return fmt.Errorf("patched constructor id: %w", err)
				}
				if err := dag.Select(ctx, updatedObj, &updatedObj, dagql.Selector{
					Field: "__withConstructor",
					Args:  []dagql.NamedInput{{Name: "function", Value: fnID}},
				}); err != nil {
					return fmt.Errorf("patch constructor enum defaults: %w", err)
				}
			}
		}
		if !sameAttachedResult(updatedObj, objRes) {
			objID, err := ResultIDInput(updatedObj)
			if err != nil {
				return fmt.Errorf("patched object typedef id: %w", err)
			}
			if err := dag.Select(ctx, obj, &mod.ObjectDefs[i], dagql.Selector{
				Field: "__withObjectTypeDef",
				Args:  []dagql.NamedInput{{Name: "objectTypeDef", Value: objID}},
			}); err != nil {
				return fmt.Errorf("patch object typedef enum defaults: %w", err)
			}
		}
	}
	for i, iface := range mod.InterfaceDefs {
		ifaceRes := iface.Self().AsInterface.Value
		updatedIface := ifaceRes
		for _, fn := range ifaceRes.Self().Functions {
			updatedFn, err := patchFunctionEnumDefaults(fn)
			if err != nil {
				return err
			}
			if sameAttachedResult(updatedFn, fn) {
				continue
			}
			fnID, err := ResultIDInput(updatedFn)
			if err != nil {
				return fmt.Errorf("patched interface function id: %w", err)
			}
			if err := dag.Select(ctx, updatedIface, &updatedIface, dagql.Selector{
				Field: "__withFunction",
				Args:  []dagql.NamedInput{{Name: "function", Value: fnID}},
			}); err != nil {
				return fmt.Errorf("patch interface function enum defaults: %w", err)
			}
		}
		if !sameAttachedResult(updatedIface, ifaceRes) {
			ifaceID, err := ResultIDInput(updatedIface)
			if err != nil {
				return fmt.Errorf("patched interface typedef id: %w", err)
			}
			if err := dag.Select(ctx, iface, &mod.InterfaceDefs[i], dagql.Selector{
				Field: "__withInterfaceTypeDef",
				Args:  []dagql.NamedInput{{Name: "interfaceTypeDef", Value: ifaceID}},
			}); err != nil {
				return fmt.Errorf("patch interface typedef enum defaults: %w", err)
			}
		}
	}
	return nil
}

func (mod *Module) LoadRuntime(ctx context.Context) (ModuleRuntime, error) {
	runtimeImpl, ok := mod.Source.Value.Self().SDKImpl.AsRuntime()
	if !ok {
		return nil, fmt.Errorf("no runtime implemented")
	}

	if !mod.Source.Valid {
		return nil, fmt.Errorf("no source")
	}

	runtime, err := runtimeImpl.Runtime(ctx, mod.Deps, mod.Source.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to load runtime: %w", err)
	}

	return runtime, nil
}

/*
Mod is a module in loaded into the server's DAG of modules; it's the vertex type of the DAG.
It's an interface so we can abstract over user modules and core and treat them the same.
*/

// InstallOpts controls how a module is installed into a dagql server.
type InstallOpts struct {
	// SkipConstructor omits the module's constructor from the Query
	// root. The module's types are still installed for schema
	// resolution. Used for transitive dependencies whose types may
	// be returned through interfaces.
	SkipConstructor bool

	// Entrypoint installs non-conflicting proxies for the module's
	// main-object methods on the Query root. The module's namespaced
	// constructor remains installed separately.
	Entrypoint bool
}

type Mod interface {
	// Name gets the name of the module
	Name() string

	// Same reports whether this module is the same installed module instance as
	// the other module.
	Same(Mod) (bool, error)

	// View gets the name of the module's view of its underlying schema
	View() (call.View, bool)

	// Install modifies the provided server to install the contents of the
	// modules declared fields.
	Install(ctx context.Context, dag *dagql.Server, opts ...InstallOpts) error

	// ModTypeFor returns the ModType for the given typedef based on this module's schema.
	// The returned type will have any namespacing already applied.
	// If checkDirectDeps is true, then its direct dependencies will also be checked.
	ModTypeFor(ctx context.Context, typeDef *TypeDef, checkDirectDeps bool) (ModType, bool, error)

	// TypeDefs gets the TypeDefs exposed by this module (not including
	// dependencies) from the given unified schema. Implicitly, TypeDefs
	// returned by this module include any extensions installed by other
	// modules from the unified schema. (e.g. LLM which is extended with each
	// type via middleware)
	TypeDefs(ctx context.Context, dag *dagql.Server) (dagql.ObjectResultArray[*TypeDef], error)

	// Source returns the ModuleSource for this module
	GetSource() *ModuleSource

	// ResultCallModule returns the native module provenance attached to calls
	// provided by this module.
	ResultCallModule(context.Context) (*dagql.ResultCallModule, error)

	// ModuleResult returns the wrapped module result for user modules, or the
	// zero value for non-module implementations like core.
	ModuleResult() dagql.ObjectResult[*Module]
}

type userMod struct {
	res dagql.ObjectResult[*Module]
}

var _ Mod = (*userMod)(nil)

func NewUserMod(modInst dagql.ObjectResult[*Module]) Mod {
	return &userMod{res: modInst}
}

func (mod *userMod) self() *Module {
	if mod == nil {
		return nil
	}
	return mod.res.Self()
}

func (mod *userMod) Name() string {
	if self := mod.self(); self != nil {
		return self.Name()
	}
	return ""
}

func (mod *userMod) Same(other Mod) (bool, error) {
	otherUser, ok := other.(*userMod)
	if !ok {
		return false, nil
	}
	id, err := mod.res.ID()
	if err != nil {
		return false, fmt.Errorf("user module %q identity: %w", mod.Name(), err)
	}
	otherID, err := otherUser.res.ID()
	if err != nil {
		return false, fmt.Errorf("user module %q identity: %w", otherUser.Name(), err)
	}
	if id == nil || otherID == nil {
		return false, nil
	}
	return id.EngineResultID() == otherID.EngineResultID(), nil
}

func (mod *userMod) View() (call.View, bool) {
	if self := mod.self(); self != nil {
		return self.View()
	}
	return "", false
}

func (mod *userMod) Install(ctx context.Context, dag *dagql.Server, opts ...InstallOpts) error {
	return mod.install(ctx, dag, opts...)
}

func (mod *userMod) ModTypeFor(ctx context.Context, typeDef *TypeDef, checkDirectDeps bool) (ModType, bool, error) {
	return mod.modTypeFor(ctx, typeDef, checkDirectDeps)
}

func (mod *userMod) TypeDefs(ctx context.Context, dag *dagql.Server) (dagql.ObjectResultArray[*TypeDef], error) {
	self := mod.self()
	if self == nil {
		return nil, fmt.Errorf("module typedefs: missing module result wrapper")
	}
	return self.TypeDefs(ctx, dag)
}

func (mod *userMod) GetSource() *ModuleSource {
	self := mod.self()
	if self == nil {
		return nil
	}
	return self.GetSource()
}

func (mod *userMod) ResultCallModule(ctx context.Context) (*dagql.ResultCallModule, error) {
	self := mod.self()
	if self == nil {
		return nil, fmt.Errorf("module provenance: missing module result wrapper")
	}
	if !self.Source.Valid {
		return nil, fmt.Errorf("module provenance: module %q has no source", self.Name())
	}

	scoped, err := ImplementationScopedModule(ctx, mod.res)
	if err != nil {
		return nil, fmt.Errorf("module provenance: implementation-scoped module %q: %w", self.Name(), err)
	}
	scopedID, err := scoped.ID()
	if err != nil {
		return nil, fmt.Errorf("module provenance: module %q handle ID: %w", self.Name(), err)
	}
	if scopedID == nil || scopedID.EngineResultID() == 0 {
		return nil, fmt.Errorf("module provenance: implementation-scoped module %q is not attached", self.Name())
	}

	src := self.Source.Value.Self()
	var ref, pin string
	switch src.Kind {
	case ModuleSourceKindLocal:
		ref = filepath.Join(src.Local.ContextDirectoryPath, src.SourceRootSubpath)
	case ModuleSourceKindGit:
		ref = src.Git.CloneRef
		if src.SourceRootSubpath != "" {
			ref += "/" + strings.TrimPrefix(src.SourceRootSubpath, "/")
		}
		if src.Git.Version != "" {
			ref += "@" + src.Git.Version
		}
		pin = src.Git.Commit
	case ModuleSourceKindDir:
	default:
		return nil, fmt.Errorf("module provenance: unexpected module source kind %q", src.Kind)
	}

	return &dagql.ResultCallModule{
		ResultRef: &dagql.ResultCallRef{ResultID: scopedID.EngineResultID()},
		Name:      self.Name(),
		Ref:       ref,
		Pin:       pin,
	}, nil
}

func (mod *userMod) ModuleResult() dagql.ObjectResult[*Module] {
	if mod == nil {
		return dagql.ObjectResult[*Module]{}
	}
	return mod.res
}

func (mod *userMod) install(ctx context.Context, dag *dagql.Server, opts ...InstallOpts) error {
	self := mod.self()
	if self == nil {
		return fmt.Errorf("install user module: missing module result wrapper")
	}

	slog.ExtraDebug("installing module", "name", self.Name())
	start := time.Now()
	defer func() { slog.ExtraDebug("done installing module", "name", self.Name(), "took", time.Since(start)) }()

	for _, def := range self.ObjectDefs {
		objDef := def.Self().AsObject.Value.Self()

		slog.ExtraDebug("installing object", "name", self.Name(), "object", objDef.Name)

		modType, ok, err := self.Deps.ModTypeFor(ctx, def.Self())
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if ok {
			if src := self.GetSource(); src != nil && src.SDK.ExperimentalFeatureEnabled(ModuleSourceExperimentalFeatureSelfCalls) {
				slog.ExtraDebug("type is already defined by dependency module", "type", objDef.Name, "module", modType.SourceMod().Name())
			} else {
				return fmt.Errorf("type %q is already defined by module %q", objDef.Name, modType.SourceMod().Name())
			}
		}

		obj := &ModuleObject{
			Module:  mod.res,
			TypeDef: objDef,
		}
		if err := obj.Install(ctx, dag, opts...); err != nil {
			return err
		}
	}

	for _, def := range self.InterfaceDefs {
		ifaceDef := def.Self().AsInterface.Value.Self()
		slog.ExtraDebug("installing interface", "name", self.Name(), "interface", ifaceDef.Name)
		iface := &InterfaceType{
			typeDef: ifaceDef,
			mod:     mod.res,
		}
		if err := iface.Install(ctx, dag); err != nil {
			return err
		}
	}

	for _, def := range self.EnumDefs {
		enumDef := def.Self().AsEnum.Value.Self()
		slog.ExtraDebug("installing enum", "name", self.Name(), "enum", enumDef.Name, "members", len(enumDef.Members))
		enum := &ModuleEnum{TypeDef: enumDef}
		enum.Install(dag)
	}

	return nil
}

func (mod *userMod) modTypeFor(ctx context.Context, typeDef *TypeDef, checkDirectDeps bool) (modType ModType, ok bool, err error) {
	self := mod.self()
	if self == nil {
		return nil, false, fmt.Errorf("module type lookup: missing module result wrapper")
	}

	switch typeDef.Kind {
	case TypeDefKindString, TypeDefKindInteger, TypeDefKindFloat, TypeDefKindBoolean, TypeDefKindVoid:
		modType, ok = self.modTypeForPrimitive(typeDef)
	case TypeDefKindList:
		modType, ok, err = mod.modTypeForList(ctx, typeDef, checkDirectDeps)
	case TypeDefKindObject:
		modType, ok, err = self.modTypeFromDeps(ctx, typeDef, checkDirectDeps)
		if ok || err != nil {
			return modType, ok, err
		}
		modType, ok = mod.modTypeForObject(typeDef)
	case TypeDefKindInterface:
		modType, ok, err = self.modTypeFromDeps(ctx, typeDef, checkDirectDeps)
		if ok || err != nil {
			return modType, ok, err
		}
		modType, ok = mod.modTypeForInterface(typeDef)
	case TypeDefKindScalar:
		modType, ok, err = self.modTypeFromDeps(ctx, typeDef, checkDirectDeps)
		if ok || err != nil {
			return modType, ok, err
		}
		modType, ok = nil, false
		slog.ExtraDebug("module did not find scalar", "mod", self.Name(), "scalar", typeDef.AsScalar.Value.Self().Name)
	case TypeDefKindEnum:
		modType, ok, err = self.modTypeFromDeps(ctx, typeDef, checkDirectDeps)
		if ok || err != nil {
			return modType, ok, err
		}
		modType, ok = mod.modTypeForEnum(typeDef)
	default:
		return nil, false, fmt.Errorf("unexpected type def kind %s", typeDef.Kind)
	}
	if err != nil {
		return nil, false, fmt.Errorf("failed to get mod type: %w", err)
	}
	if !ok {
		return nil, false, nil
	}

	if typeDef.Optional {
		innerDef, err := modType.TypeDef(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("resolve nullable inner typedef: %w", err)
		}
		if innerDef.Self().Optional {
			dag, err := CurrentDagqlServer(ctx)
			if err != nil {
				return nil, false, fmt.Errorf("current dagql server for nullable inner typedef: %w", err)
			}
			if err := dag.Select(ctx, innerDef, &innerDef, dagql.Selector{
				Field: "withOptional",
				Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(false)}},
			}); err != nil {
				return nil, false, fmt.Errorf("clear optional on nullable inner typedef: %w", err)
			}
		}
		modType = &NullableType{
			InnerDef: innerDef,
			Inner:    modType,
		}
	}

	return modType, true, nil
}

func (mod *userMod) modTypeForList(ctx context.Context, typedef *TypeDef, checkDirectDeps bool) (ModType, bool, error) {
	underlyingType, ok, err := mod.modTypeFor(ctx, typedef.AsList.Value.Self().ElementTypeDef.Self(), checkDirectDeps)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get underlying type: %w", err)
	}
	if !ok {
		return nil, false, nil
	}

	return &ListType{
		Elem:       typedef.AsList.Value.Self().ElementTypeDef,
		Underlying: underlyingType,
	}, true, nil
}

func (mod *userMod) modTypeForObject(typeDef *TypeDef) (ModType, bool) {
	self := mod.self()
	for _, obj := range self.ObjectDefs {
		if obj.Self().AsObject.Value.Self().Name == typeDef.AsObject.Value.Self().Name {
			return &ModuleObjectType{
				typeDef: obj.Self().AsObject.Value.Self(),
				mod:     mod.res,
			}, true
		}
	}

	slog.Trace("module did not find object", "mod", self.Name(), "object", typeDef.AsObject.Value.Self().Name)
	return nil, false
}

func (mod *userMod) modTypeForInterface(typeDef *TypeDef) (ModType, bool) {
	self := mod.self()
	for _, iface := range self.InterfaceDefs {
		if iface.Self().AsInterface.Value.Self().Name == typeDef.AsInterface.Value.Self().Name {
			return &InterfaceType{
				typeDef: iface.Self().AsInterface.Value.Self(),
				mod:     mod.res,
			}, true
		}
	}

	slog.Trace("module did not find interface", "mod", self.Name(), "interface", typeDef.AsInterface.Value.Self().Name)
	return nil, false
}

func (mod *userMod) modTypeForEnum(typeDef *TypeDef) (ModType, bool) {
	self := mod.self()
	for _, enum := range self.EnumDefs {
		if enum.Self().AsEnum.Value.Self().Name == typeDef.AsEnum.Value.Self().Name {
			return &ModuleEnumType{
				typeDef: enum.Self().AsEnum.Value.Self(),
				mod:     mod.res,
			}, true
		}
	}

	slog.Trace("module did not find enum", "mod", self.Name(), "enum", typeDef.AsEnum.Value.Self().Name)
	return nil, false
}

func (mod Module) Clone() *Module {
	cp := mod

	if mod.SDKConfig != nil {
		cp.SDKConfig = mod.SDKConfig.Clone()
	}

	if mod.Deps != nil {
		cp.Deps = mod.Deps.Clone()
	}

	cp.ObjectDefs = append(dagql.ObjectResultArray[*TypeDef](nil), mod.ObjectDefs...)
	cp.InterfaceDefs = append(dagql.ObjectResultArray[*TypeDef](nil), mod.InterfaceDefs...)
	cp.EnumDefs = append(dagql.ObjectResultArray[*TypeDef](nil), mod.EnumDefs...)

	if cp.SDKConfig != nil {
		cp.SDKConfig = cp.SDKConfig.Clone()
	}

	if mod.WorkspaceConfig != nil {
		cp.WorkspaceConfig = make(map[string]any, len(mod.WorkspaceConfig))
		for k, v := range mod.WorkspaceConfig {
			cp.WorkspaceConfig[k] = v
		}
	}
	return &cp
}

func (mod Module) CloneWithoutDefs() *Module {
	cp := mod.Clone()

	cp.EnumDefs = dagql.ObjectResultArray[*TypeDef]{}
	cp.ObjectDefs = dagql.ObjectResultArray[*TypeDef]{}
	cp.InterfaceDefs = dagql.ObjectResultArray[*TypeDef]{}

	return cp
}

func (mod *Module) WithDescription(desc string) *Module {
	mod = mod.Clone()
	mod.Description = strings.TrimSpace(desc)
	return mod
}

func (mod *Module) WithObject(ctx context.Context, def dagql.ObjectResult[*TypeDef]) (*Module, error) {
	mod = mod.Clone()

	if !def.Self().AsObject.Valid {
		return nil, fmt.Errorf("expected object type def, got %s: %+v", def.Self().Kind, def.Self())
	}

	// skip validation+namespacing for module objects being constructed by SDK with* calls
	// they will be validated when merged into the real final module

	if mod.Deps != nil {
		if err := mod.validateTypeDef(ctx, def, mod.newValidationState()); err != nil {
			return nil, fmt.Errorf("failed to validate type def: %w", err)
		}
	}
	if mod.NameField != "" {
		modPath := mod.modulePath()
		var err error
		def, err = mod.namespaceTypeDef(ctx, modPath, def)
		if err != nil {
			return nil, fmt.Errorf("failed to namespace type def: %w", err)
		}
	}

	mod.ObjectDefs = append(mod.ObjectDefs, def)
	return mod, nil
}

func (mod *Module) WithInterface(ctx context.Context, def dagql.ObjectResult[*TypeDef]) (*Module, error) {
	mod = mod.Clone()
	if !def.Self().AsInterface.Valid {
		return nil, fmt.Errorf("expected interface type def, got %s: %+v", def.Self().Kind, def.Self())
	}

	// skip validation+namespacing for module objects being constructed by SDK with* calls
	// they will be validated when merged into the real final module

	if mod.Deps != nil {
		if err := mod.validateTypeDef(ctx, def, mod.newValidationState()); err != nil {
			return nil, fmt.Errorf("failed to validate type def: %w", err)
		}
	}
	if mod.NameField != "" {
		modPath := mod.modulePath()
		var err error
		def, err = mod.namespaceTypeDef(ctx, modPath, def)
		if err != nil {
			return nil, fmt.Errorf("failed to namespace type def: %w", err)
		}
	}

	mod.InterfaceDefs = append(mod.InterfaceDefs, def)
	return mod, nil
}

func (mod *Module) WithEnum(ctx context.Context, def dagql.ObjectResult[*TypeDef]) (*Module, error) {
	mod = mod.Clone()
	if !def.Self().AsEnum.Valid {
		return nil, fmt.Errorf("expected enum type def, got %s: %+v", def.Self().Kind, def.Self())
	}

	// skip validation+namespacing for module objects being constructed by SDK with* calls
	// they will be validated when merged into the real final module

	if mod.Deps != nil {
		if err := mod.validateTypeDef(ctx, def, mod.newValidationState()); err != nil {
			return nil, fmt.Errorf("failed to validate type def: %w", err)
		}
	}
	if mod.NameField != "" {
		modPath := mod.modulePath()
		var err error
		def, err = mod.namespaceTypeDef(ctx, modPath, def)
		if err != nil {
			return nil, fmt.Errorf("failed to namespace type def: %w", err)
		}
	}

	mod.EnumDefs = append(mod.EnumDefs, def)

	return mod, nil
}

type CurrentModule struct {
	Module dagql.ObjectResult[*Module]
}

func (*CurrentModule) Type() *ast.Type {
	return &ast.Type{
		NamedType: "CurrentModule",
		NonNull:   true,
	}
}

func (*CurrentModule) TypeDescription() string {
	return "Reflective module API provided to functions at runtime."
}

func (mod CurrentModule) Clone() *CurrentModule {
	cp := mod
	if mod.Module.Self() != nil {
		cp.Module = mod.Module
	}
	return &cp
}
