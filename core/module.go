package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
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

	// The source to load contextual dirs/files from, which may be different than Source for blueprints or toolchains
	ContextSource dagql.Nullable[dagql.ObjectResult[*ModuleSource]]

	// The name of the module
	NameField string `field:"true" name:"name" doc:"The name of the module"`

	// The original name of the module set in its configuration file (or first configured via withName).
	// Different than NameField when a different name was specified for the module via a dependency.
	OriginalName string

	// The module's SDKConfig, as set in the module config file
	SDKConfig *SDKConfig `field:"true" name:"sdk" doc:"The SDK config used by this module."`

	// Deps contains the module's dependency DAG.
	Deps *ModDeps

	// Runtime is the container that runs the module's entrypoint. It will fail to execute if the module doesn't compile.
	Runtime dagql.Nullable[dagql.ObjectResult[*Container]]

	// The following are populated while initializing the module

	// The doc string of the module, if any
	Description string `field:"true" doc:"The doc string of the module, if any"`

	// The module's objects
	ObjectDefs []*TypeDef `field:"true" name:"objects" doc:"Objects served by this module."`

	// The module's interfaces
	InterfaceDefs []*TypeDef `field:"true" name:"interfaces" doc:"Interfaces served by this module."`

	// The module's enumerations
	EnumDefs []*TypeDef `field:"true" name:"enums" doc:"Enumerations served by this module."`

	// IsToolchain indicates this module was loaded as a toolchain dependency.
	// Toolchain modules are allowed to share types with the modules that depend on them.
	IsToolchain bool

	// Toolchains manages all toolchain modules and their configuration.
	Toolchains *ToolchainRegistry

	persistedResultID uint64
	includeSelfInDeps bool

	// If true, disable the new default function caching behavior for this module. Functions will
	// instead default to the old behavior of per-session caching.
	DisableDefaultFunctionCaching bool
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
var _ dagql.HasOwnedResults = (*Module)(nil)

func (mod *Module) Name() string {
	return mod.NameField
}

func (mod *Module) PersistedResultID() uint64 {
	if mod == nil {
		return 0
	}
	return mod.persistedResultID
}

func (mod *Module) SetPersistedResultID(resultID uint64) {
	if mod != nil {
		mod.persistedResultID = resultID
	}
}

// isToolchainModule checks if a Mod is a toolchain Module.
// This centralizes the validation logic that was scattered across the codebase.
func isToolchainModule(m Mod) bool {
	tcMod, ok := m.(*userMod)
	return ok && tcMod.self() != nil && tcMod.self().IsToolchain
}

func (mod *Module) MainObject() (*ObjectTypeDef, bool) {
	return mod.ObjectByName(mod.Name())
}

func (mod *Module) ObjectByName(name string) (*ObjectTypeDef, bool) {
	for _, objDef := range mod.ObjectDefs {
		if objDef.AsObject.Valid {
			obj := objDef.AsObject.Value
			if gqlObjectName(obj.Name) == gqlObjectName(name) {
				return obj, true
			}
		}
	}
	return nil, false
}

func functionRequiresArgs(fn *Function) bool {
	for _, arg := range fn.Args {
		// NOTE: we count on user defaults already merged in the schema at this point
		// "regular optional" -> ok
		if arg.TypeDef.Optional {
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

// Return all user defaults for this module
func (mod *Module) UserDefaults(ctx context.Context) (*EnvFile, error) {
	defaults := NewEnvFile(true)

	// Use ContextSource for loading .env files (it has the actual context directory)
	// but use Source for the module name prefix lookups
	contextSrc := mod.GetContextSource()
	if contextSrc != nil && contextSrc.UserDefaults != nil {
		defaults = defaults.WithEnvFiles(contextSrc.UserDefaults)
	}

	src := mod.GetSource()
	if src != nil && src != contextSrc && src.UserDefaults != nil {
		// Also merge in toolchain source defaults if different from context
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

func (mod *Module) Evaluate(context.Context) error {
	return nil
}

func (mod *Module) Sync(ctx context.Context) error {
	return mod.Evaluate(ctx)
}

func (mod *Module) AttachOwnedResults(
	ctx context.Context,
	self dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if mod == nil {
		return nil, nil
	}

	owned := make([]dagql.AnyResult, 0, 3)

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
		for i, dep := range mod.Deps.mods {
			depInst := dep.ModuleResult()
			if depInst.Self() == nil {
				continue
			}
			attachedInst, attachedRes, err := attachModuleRef(depInst)
			if err != nil {
				return nil, fmt.Errorf("attach module dependency %q: %w", dep.Name(), err)
			}
			if attachedRes == nil {
				continue
			}
			mod.Deps.mods[i] = NewUserMod(attachedInst)
			owned = append(owned, attachedRes)
		}
	}
	if mod.includeSelfInDeps {
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
		for i, dep := range mod.Deps.mods {
			depInst := dep.ModuleResult()
			if depInst.Self() == nil {
				continue
			}
			depID, err := depInst.ID()
			if err != nil {
				return nil, fmt.Errorf("attach module self dependency %q: dep ID: %w", dep.Name(), err)
			}
			if depID == nil || depID.Digest() != attachedSelfID.Digest() {
				continue
			}
			mod.Deps.mods[i] = NewUserMod(attachedSelf)
			seenSelf = true
			break
		}
		if !seenSelf {
			mod.Deps = mod.Deps.Append(NewUserMod(attachedSelf))
		}
	}
	if mod.Toolchains != nil {
		for name, entry := range mod.Toolchains.entries {
			if entry == nil || entry.Module.Self() == nil {
				continue
			}
			_, attachedRes, err := attachModuleRef(entry.Module)
			if err != nil {
				return nil, fmt.Errorf("attach module toolchain %q: %w", name, err)
			}
			if attachedRes == nil {
				continue
			}
			entry.Module = attachedRes.(dagql.ObjectResult[*Module])
			owned = append(owned, attachedRes)
		}
	}

	return owned, nil
}

type persistedModulePayload struct {
	SourceResultID                uint64                          `json:"sourceResultID,omitempty"`
	ContextSourceResultID         uint64                          `json:"contextSourceResultID,omitempty"`
	RuntimeResultID               uint64                          `json:"runtimeResultID,omitempty"`
	DepModuleResultIDs            []uint64                        `json:"depModuleResultIDs,omitempty"`
	ToolchainsInitialized         bool                            `json:"toolchainsInitialized,omitempty"`
	ToolchainEntries              []persistedModuleToolchainEntry `json:"toolchainEntries,omitempty"`
	IncludeSelfInDeps             bool                            `json:"includeSelfInDeps,omitempty"`
	NameField                     string                          `json:"nameField,omitempty"`
	OriginalName                  string                          `json:"originalName,omitempty"`
	SDKConfig                     *SDKConfig                      `json:"sdkConfig,omitempty"`
	Description                   string                          `json:"description,omitempty"`
	ObjectDefs                    []*TypeDef                      `json:"objectDefs,omitempty"`
	InterfaceDefs                 []*TypeDef                      `json:"interfaceDefs,omitempty"`
	EnumDefs                      []*TypeDef                      `json:"enumDefs,omitempty"`
	IsToolchain                   bool                            `json:"isToolchain,omitempty"`
	DisableDefaultFunctionCaching bool                            `json:"disableDefaultFunctionCaching,omitempty"`
}

type persistedModuleToolchainEntry struct {
	Name             string                          `json:"name,omitempty"`
	ModuleResultID   uint64                          `json:"moduleResultID,omitempty"`
	FieldName        string                          `json:"fieldName,omitempty"`
	ArgumentConfigs  []*modules.ModuleConfigArgument `json:"argumentConfigs,omitempty"`
	IgnoreChecks     []string                        `json:"ignoreChecks,omitempty"`
	IgnoreGenerators []string                        `json:"ignoreGenerators,omitempty"`
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

	persisted.IncludeSelfInDeps = mod.includeSelfInDeps
	if mod.Deps != nil {
		persisted.DepModuleResultIDs = make([]uint64, 0, len(mod.Deps.Mods()))
		for _, dep := range mod.Deps.Mods() {
			depInst := dep.ModuleResult()
			if depInst.Self() == nil {
				continue
			}
			if mod.includeSelfInDeps && depInst.Self() == mod {
				continue
			}
			if depInst.Self().PersistedResultID() == 0 {
				return nil, fmt.Errorf("encode persisted module dependency %q: missing persisted result ID", dep.Name())
			}
			persisted.DepModuleResultIDs = append(persisted.DepModuleResultIDs, depInst.Self().PersistedResultID())
		}
	}

	persisted.ToolchainsInitialized = mod.Toolchains != nil
	if mod.Toolchains != nil {
		toolchainNames := make([]string, 0, len(mod.Toolchains.entries))
		for name := range mod.Toolchains.entries {
			toolchainNames = append(toolchainNames, name)
		}
		sort.Strings(toolchainNames)
		persisted.ToolchainEntries = make([]persistedModuleToolchainEntry, 0, len(toolchainNames))
		for _, name := range toolchainNames {
			entry := mod.Toolchains.entries[name]
			if entry == nil || entry.Module.Self() == nil {
				return nil, fmt.Errorf("encode persisted module toolchain %q: missing module result ID", name)
			}
			if entry.Module.Self().PersistedResultID() == 0 {
				return nil, fmt.Errorf("encode persisted module toolchain %q: missing persisted module result ID", name)
			}
			persisted.ToolchainEntries = append(persisted.ToolchainEntries, persistedModuleToolchainEntry{
				Name:             name,
				ModuleResultID:   entry.Module.Self().PersistedResultID(),
				FieldName:        entry.FieldName,
				ArgumentConfigs:  entry.ArgumentConfigs,
				IgnoreChecks:     entry.IgnoreChecks,
				IgnoreGenerators: entry.IgnoreGenerators,
			})
		}
	}

	persisted.NameField = mod.NameField
	persisted.OriginalName = mod.OriginalName
	persisted.SDKConfig = mod.SDKConfig
	persisted.Description = mod.Description
	persisted.ObjectDefs = mod.ObjectDefs
	persisted.InterfaceDefs = mod.InterfaceDefs
	persisted.EnumDefs = mod.EnumDefs
	persisted.IsToolchain = mod.IsToolchain
	persisted.DisableDefaultFunctionCaching = mod.DisableDefaultFunctionCaching

	objectSummaries := make([]string, 0, len(mod.ObjectDefs))
	for _, def := range mod.ObjectDefs {
		if !def.AsObject.Valid {
			continue
		}
		obj := def.AsObject.Value
		fnNames := make([]string, 0, len(obj.Functions))
		for _, fn := range obj.Functions {
			fnNames = append(fnNames, fn.Name)
		}
		objectSummaries = append(objectSummaries, fmt.Sprintf("%s:%v", obj.Name, fnNames))
	}
	slog.Info(
		"cache-debug module persist encode",
		"module", mod.Name(),
		"object_defs", len(mod.ObjectDefs),
		"objects", objectSummaries,
	)

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

	loadedDepMods := make(map[uint64]dagql.ObjectResult[*Module], len(persisted.DepModuleResultIDs))
	for _, depID := range persisted.DepModuleResultIDs {
		depRes, err := loadPersistedObjectResultByResultID[*Module](ctx, dag, depID, "module dependency")
		if err != nil {
			return nil, err
		}
		deps = deps.Append(NewUserMod(depRes))
		loadedDepMods[depID] = depRes
	}

	mod := &Module{
		NameField:                     persisted.NameField,
		OriginalName:                  persisted.OriginalName,
		SDKConfig:                     persisted.SDKConfig,
		Deps:                          deps,
		Description:                   persisted.Description,
		ObjectDefs:                    persisted.ObjectDefs,
		InterfaceDefs:                 persisted.InterfaceDefs,
		EnumDefs:                      persisted.EnumDefs,
		IsToolchain:                   persisted.IsToolchain,
		includeSelfInDeps:             persisted.IncludeSelfInDeps,
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
	if persisted.ToolchainsInitialized {
		mod.Toolchains = NewToolchainRegistry(mod)
		for _, persistedEntry := range persisted.ToolchainEntries {
			tcRes := loadedDepMods[persistedEntry.ModuleResultID]
			if tcRes.Self() == nil {
				var err error
				tcRes, err = loadPersistedObjectResultByResultID[*Module](ctx, dag, persistedEntry.ModuleResultID, "module toolchain")
				if err != nil {
					return nil, err
				}
				mod.Deps = mod.Deps.Append(NewUserMod(tcRes))
				loadedDepMods[persistedEntry.ModuleResultID] = tcRes
			}
			mod.Toolchains.entries[persistedEntry.Name] = &ToolchainEntry{
				Module:           tcRes,
				FieldName:        persistedEntry.FieldName,
				ArgumentConfigs:  persistedEntry.ArgumentConfigs,
				IgnoreChecks:     persistedEntry.IgnoreChecks,
				IgnoreGenerators: persistedEntry.IgnoreGenerators,
			}
		}
	}

	objectSummaries := make([]string, 0, len(mod.ObjectDefs))
	for _, def := range mod.ObjectDefs {
		if !def.AsObject.Valid {
			continue
		}
		obj := def.AsObject.Value
		fnNames := make([]string, 0, len(obj.Functions))
		for _, fn := range obj.Functions {
			fnNames = append(fnNames, fn.Name)
		}
		objectSummaries = append(objectSummaries, fmt.Sprintf("%s:%v", obj.Name, fnNames))
	}
	slog.Info(
		"cache-debug module persist decode",
		"module", mod.Name(),
		"object_defs", len(mod.ObjectDefs),
		"objects", objectSummaries,
	)

	return mod, nil
}

func (mod *Module) TypeDefs(ctx context.Context, dag *dagql.Server) ([]*TypeDef, error) {
	// TODO: use dag arg to reflect dynamic updates (if/when we support that)

	typeDefs := make([]*TypeDef, 0, len(mod.ObjectDefs)+len(mod.InterfaceDefs)+len(mod.EnumDefs))

	for _, def := range mod.ObjectDefs {
		typeDef := def.Clone()
		if typeDef.AsObject.Valid {
			typeDef.AsObject.Value.SourceModuleName = mod.Name()
		}
		typeDefs = append(typeDefs, typeDef)
	}

	for _, def := range mod.InterfaceDefs {
		typeDef := def.Clone()
		if typeDef.AsInterface.Valid {
			typeDef.AsInterface.Value.SourceModuleName = mod.Name()
		}
		typeDefs = append(typeDefs, typeDef)
	}

	for _, def := range mod.EnumDefs {
		typeDef := def.Clone()
		if typeDef.AsEnum.Valid {
			typeDef.AsEnum.Value.SourceModuleName = mod.Name()
		}
		typeDefs = append(typeDefs, typeDef)
	}

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

// verify the typedef is has no reserved names
func (mod *Module) validateTypeDef(ctx context.Context, typeDef *TypeDef) error {
	switch typeDef.Kind {
	case TypeDefKindList:
		return mod.validateTypeDef(ctx, typeDef.AsList.Value.ElementTypeDef)
	case TypeDefKindObject:
		return mod.validateObjectTypeDef(ctx, typeDef)
	case TypeDefKindInterface:
		return mod.validateInterfaceTypeDef(ctx, typeDef)
	}
	return nil
}

//nolint:gocyclo
func (mod *Module) validateObjectTypeDef(ctx context.Context, typeDef *TypeDef) error {
	// check whether this is a pre-existing object from core or another module
	modType, ok, err := mod.Deps.ModTypeFor(ctx, typeDef)
	if err != nil {
		return fmt.Errorf("failed to get mod type for type def: %w", err)
	}
	if ok {
		if sourceMod := modType.SourceMod(); sourceMod != nil && sourceMod.Name() != mod.Name() {
			// already validated, skip
			return nil
		}
	}

	obj := typeDef.AsObject.Value

	for _, field := range obj.Fields {
		if gqlFieldName(field.Name) == "id" {
			return fmt.Errorf("cannot define field with reserved name %q on object %q", field.Name, obj.Name)
		}
		// Workspace cannot be stored as a field on a module object
		if field.TypeDef.Kind == TypeDefKindObject && field.TypeDef.AsObject.Value.Name == "Workspace" {
			return fmt.Errorf("object %q field %q: Workspace cannot be stored as a field on a module object; declare it as a function argument instead",
				obj.OriginalName,
				field.OriginalName,
			)
		}
		fieldType, ok, err := mod.Deps.ModTypeFor(ctx, field.TypeDef)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if ok {
			sourceMod := fieldType.SourceMod()
			// fields can reference core types and local types, but not types from
			// other modules (unless the source module is a toolchain)
			if sourceMod != nil && sourceMod.Name() != ModuleName && sourceMod.Name() != mod.Name() {
				// Allow types from toolchain modules
				if !isToolchainModule(sourceMod) {
					return fmt.Errorf("object %q field %q cannot reference external type from dependency module %q",
						obj.OriginalName,
						field.OriginalName,
						sourceMod.Name(),
					)
				}
			}
		}
		if err := mod.validateTypeDef(ctx, field.TypeDef); err != nil {
			return err
		}
	}

	for fn := range obj.functions() {
		if gqlFieldName(fn.Name) == "id" {
			return fmt.Errorf("cannot define function with reserved name %q on object %q", fn.Name, obj.Name)
		}
		// Check if this is a type from another (non-core) module
		retType, ok, err := mod.Deps.ModTypeFor(ctx, fn.ReturnType)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if ok {
			if sourceMod := retType.SourceMod(); sourceMod != nil && sourceMod.Name() != ModuleName && sourceMod.Name() != mod.Name() {
				// Allow types from toolchain modules
				if !isToolchainModule(sourceMod) {
					return fmt.Errorf("object %q function %q cannot return external type from dependency module %q",
						obj.OriginalName,
						fn.OriginalName,
						sourceMod.Name(),
					)
				}
			}
		}
		if err := mod.validateTypeDef(ctx, fn.ReturnType); err != nil {
			return err
		}

		for _, arg := range fn.Args {
			argType, ok, err := mod.Deps.ModTypeFor(ctx, arg.TypeDef)
			if err != nil {
				return fmt.Errorf("failed to get mod type for type def: %w", err)
			}
			if ok {
				if sourceMod := argType.SourceMod(); sourceMod != nil && sourceMod.Name() != ModuleName && sourceMod.Name() != mod.Name() {
					// Allow types from toolchain modules
					if !isToolchainModule(sourceMod) {
						return fmt.Errorf("object %q function %q arg %q cannot reference external type from dependency module %q",
							obj.OriginalName,
							fn.OriginalName,
							arg.OriginalName,
							sourceMod.Name(),
						)
					}
				}
			}
			if err := mod.validateTypeDef(ctx, arg.TypeDef); err != nil {
				return err
			}
		}
	}
	return nil
}

func (mod *Module) validateInterfaceTypeDef(ctx context.Context, typeDef *TypeDef) error {
	iface := typeDef.AsInterface.Value

	// check whether this is a pre-existing interface from core or another module
	modType, ok, err := mod.Deps.ModTypeFor(ctx, typeDef)
	if err != nil {
		return fmt.Errorf("failed to get mod type for type def: %w", err)
	}
	if ok {
		if sourceMod := modType.SourceMod(); sourceMod != nil && sourceMod.Name() != mod.Name() {
			// already validated, skip
			return nil
		}
	}
	for _, fn := range iface.Functions {
		if gqlFieldName(fn.Name) == "id" {
			return fmt.Errorf("cannot define function with reserved name %q on interface %q", fn.Name, iface.Name)
		}
		if err := mod.validateTypeDef(ctx, fn.ReturnType); err != nil {
			return err
		}

		for _, arg := range fn.Args {
			if err := mod.validateTypeDef(ctx, arg.TypeDef); err != nil {
				return err
			}
		}
	}
	return nil
}

// prefix the given typedef (and any recursively referenced typedefs) with this
// module's name/path for any objects
func (mod *Module) namespaceTypeDef(ctx context.Context, modPath string, typeDef *TypeDef) error {
	switch typeDef.Kind {
	case TypeDefKindList:
		if err := mod.namespaceTypeDef(ctx, modPath, typeDef.AsList.Value.ElementTypeDef); err != nil {
			return err
		}
	case TypeDefKindObject:
		obj := typeDef.AsObject.Value

		// only namespace objects defined in this module
		_, ok, err := mod.Deps.ModTypeFor(ctx, typeDef)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if !ok {
			obj.Name = namespaceObject(obj.OriginalName, mod.Name(), mod.OriginalName)
			obj.SourceMap = mod.namespaceSourceMap(modPath, obj.SourceMap)
		}

		for _, field := range obj.Fields {
			if err := mod.namespaceTypeDef(ctx, modPath, field.TypeDef); err != nil {
				return err
			}
			field.SourceMap = mod.namespaceSourceMap(modPath, field.SourceMap)
		}

		for fn := range obj.functions() {
			if err := mod.namespaceTypeDef(ctx, modPath, fn.ReturnType); err != nil {
				return err
			}
			fn.SourceMap = mod.namespaceSourceMap(modPath, fn.SourceMap)

			for _, arg := range fn.Args {
				if err := mod.namespaceTypeDef(ctx, modPath, arg.TypeDef); err != nil {
					return err
				}
				arg.SourceMap = mod.namespaceSourceMap(modPath, arg.SourceMap)
			}
		}

	case TypeDefKindInterface:
		iface := typeDef.AsInterface.Value

		// only namespace interfaces defined in this module
		_, ok, err := mod.Deps.ModTypeFor(ctx, typeDef)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if !ok {
			iface.Name = namespaceObject(iface.OriginalName, mod.Name(), mod.OriginalName)
			iface.SourceMap = mod.namespaceSourceMap(modPath, iface.SourceMap)
		}

		for _, fn := range iface.Functions {
			if err := mod.namespaceTypeDef(ctx, modPath, fn.ReturnType); err != nil {
				return err
			}
			fn.SourceMap = mod.namespaceSourceMap(modPath, fn.SourceMap)

			for _, arg := range fn.Args {
				if err := mod.namespaceTypeDef(ctx, modPath, arg.TypeDef); err != nil {
					return err
				}
				arg.SourceMap = mod.namespaceSourceMap(modPath, arg.SourceMap)
			}
		}
	case TypeDefKindEnum:
		enum := typeDef.AsEnum.Value

		// only namespace enums defined in this module
		mtype, ok, err := mod.Deps.ModTypeFor(ctx, typeDef)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if ok {
			enum.Members = mtype.TypeDef().AsEnum.Value.Members
		}
		if !ok {
			enum.Name = namespaceObject(enum.OriginalName, mod.Name(), mod.OriginalName)
			enum.SourceMap = mod.namespaceSourceMap(modPath, enum.SourceMap)
		}

		for _, value := range enum.Members {
			value.SourceMap = mod.namespaceSourceMap(modPath, value.SourceMap)
		}
	}
	return nil
}

func (mod *Module) namespaceSourceMap(modPath string, sourceMap dagql.Nullable[*SourceMap]) dagql.Nullable[*SourceMap] {
	if !sourceMap.Valid {
		return sourceMap
	}

	sourceMap.Value.Module = mod.Name()
	sourceMap.Value.Filename = filepath.Join(modPath, sourceMap.Value.Filename)

	if mod.Source.Value.Self().Kind == ModuleSourceKindGit {
		link, err := mod.Source.Value.Self().Git.Link(sourceMap.Value.Filename, sourceMap.Value.Line, sourceMap.Value.Column)
		if err != nil {
			return dagql.Null[*SourceMap]()
		}
		sourceMap.Value.URL = link
	}

	return sourceMap
}

// modulePath gets the prefix for the file sourcemaps, so that the sourcemap is
// relative to the context directory
func (mod *Module) modulePath() string {
	return mod.Source.Value.Self().SourceSubpath
}

// Patch is called after all types have been loaded - here we can update any
// definitions as required, and attempt to resolve references.
func (mod *Module) Patch() error {
	// patch a function's default arguments so that the default value
	// correctly matches the Name, not the OriginalName (simplifies a lot of
	// code downstream, and makes type introspection make sense)
	patchFunctionEnumDefaults := func(fn *Function) error {
		for _, arg := range fn.Args {
			if arg.DefaultValue == nil {
				continue
			}
			if arg.TypeDef.Kind != TypeDefKindEnum {
				continue
			}
			var enumTypeDef *EnumTypeDef
			for _, enum := range mod.EnumDefs {
				if enum.AsEnum.Value.Name == arg.TypeDef.AsEnum.Value.Name {
					enumTypeDef = enum.AsEnum.Value
					break
				}
			}
			if enumTypeDef == nil {
				continue
			}

			var val string
			dec := json.NewDecoder(bytes.NewReader(arg.DefaultValue.Bytes()))
			dec.UseNumber()
			if err := dec.Decode(&val); err != nil {
				return fmt.Errorf("failed to decode default value for arg %q: %w", arg.Name, err)
			}

			found := false
			for _, member := range enumTypeDef.Members {
				if val == member.OriginalName {
					val = member.Name
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("enum name %q not found", val)
			}

			res, err := json.Marshal(val)
			if err != nil {
				return err
			}
			arg.DefaultValue = JSON(res)
		}
		return nil
	}
	for _, obj := range mod.ObjectDefs {
		for fn := range obj.AsObject.Value.functions() {
			patchFunctionEnumDefaults(fn)
		}
	}
	for _, obj := range mod.InterfaceDefs {
		for _, fn := range obj.AsInterface.Value.Functions {
			patchFunctionEnumDefaults(fn)
		}
	}
	return nil
}

func (mod *Module) LoadRuntime(ctx context.Context) (runtime dagql.ObjectResult[*Container], err error) {
	runtimeImpl, ok := mod.Source.Value.Self().SDKImpl.AsRuntime()
	if !ok {
		return runtime, fmt.Errorf("no runtime implemented")
	}

	if !mod.Source.Valid {
		return runtime, fmt.Errorf("no source")
	}

	runtime, err = runtimeImpl.Runtime(ctx, mod.Deps, mod.Source.Value)
	if err != nil {
		return runtime, fmt.Errorf("failed to load runtime: %w", err)
	}

	return runtime, nil
}

/*
Mod is a module in loaded into the server's DAG of modules; it's the vertex type of the DAG.
It's an interface so we can abstract over user modules and core and treat them the same.
*/
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
	Install(ctx context.Context, dag *dagql.Server) error

	// ModTypeFor returns the ModType for the given typedef based on this module's schema.
	// The returned type will have any namespacing already applied.
	// If checkDirectDeps is true, then its direct dependencies will also be checked.
	ModTypeFor(ctx context.Context, typeDef *TypeDef, checkDirectDeps bool) (ModType, bool, error)

	// TypeDefs gets the TypeDefs exposed by this module (not including
	// dependencies) from the given unified schema. Implicitly, TypeDefs
	// returned by this module include any extensions installed by other
	// modules from the unified schema. (e.g. LLM which is extended with each
	// type via middleware)
	TypeDefs(ctx context.Context, dag *dagql.Server) ([]*TypeDef, error)

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
	return id.Digest() == otherID.Digest(), nil
}

func (mod *userMod) View() (call.View, bool) {
	if self := mod.self(); self != nil {
		return self.View()
	}
	return "", false
}

func (mod *userMod) Install(ctx context.Context, dag *dagql.Server) error {
	return mod.install(ctx, dag)
}

func (mod *userMod) ModTypeFor(ctx context.Context, typeDef *TypeDef, checkDirectDeps bool) (ModType, bool, error) {
	return mod.modTypeFor(ctx, typeDef, checkDirectDeps)
}

func (mod *userMod) TypeDefs(ctx context.Context, dag *dagql.Server) ([]*TypeDef, error) {
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

func (mod *userMod) install(ctx context.Context, dag *dagql.Server) error {
	self := mod.self()
	if self == nil {
		return fmt.Errorf("install user module: missing module result wrapper")
	}

	slog.ExtraDebug("installing module", "name", self.Name())
	start := time.Now()
	defer func() { slog.ExtraDebug("done installing module", "name", self.Name(), "took", time.Since(start)) }()

	for _, def := range self.ObjectDefs {
		objDef := def.AsObject.Value

		slog.ExtraDebug("installing object", "name", self.Name(), "object", objDef.Name)

		modType, ok, err := self.Deps.ModTypeFor(ctx, def)
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
		if err := obj.Install(ctx, dag); err != nil {
			return err
		}
	}

	for _, def := range self.InterfaceDefs {
		ifaceDef := def.AsInterface.Value
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
		enumDef := def.AsEnum.Value
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
		slog.ExtraDebug("module did not find scalar", "mod", self.Name(), "scalar", typeDef.AsScalar.Value.Name)
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
		modType = &NullableType{
			InnerDef: modType.TypeDef().WithOptional(false),
			Inner:    modType,
		}
	}

	return modType, true, nil
}

func (mod *userMod) modTypeForList(ctx context.Context, typedef *TypeDef, checkDirectDeps bool) (ModType, bool, error) {
	underlyingType, ok, err := mod.modTypeFor(ctx, typedef.AsList.Value.ElementTypeDef, checkDirectDeps)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get underlying type: %w", err)
	}
	if !ok {
		return nil, false, nil
	}

	return &ListType{
		Elem:       typedef.AsList.Value.ElementTypeDef,
		Underlying: underlyingType,
	}, true, nil
}

func (mod *userMod) modTypeForObject(typeDef *TypeDef) (ModType, bool) {
	self := mod.self()
	for _, obj := range self.ObjectDefs {
		if obj.AsObject.Value.Name == typeDef.AsObject.Value.Name {
			return &ModuleObjectType{
				typeDef: obj.AsObject.Value,
				mod:     mod.res,
			}, true
		}
	}

	slog.Trace("module did not find object", "mod", self.Name(), "object", typeDef.AsObject.Value.Name)
	return nil, false
}

func (mod *userMod) modTypeForInterface(typeDef *TypeDef) (ModType, bool) {
	self := mod.self()
	for _, iface := range self.InterfaceDefs {
		if iface.AsInterface.Value.Name == typeDef.AsInterface.Value.Name {
			return &InterfaceType{
				typeDef: iface.AsInterface.Value,
				mod:     mod.res,
			}, true
		}
	}

	slog.Trace("module did not find interface", "mod", self.Name(), "interface", typeDef.AsInterface.Value.Name)
	return nil, false
}

func (mod *userMod) modTypeForEnum(typeDef *TypeDef) (ModType, bool) {
	self := mod.self()
	for _, enum := range self.EnumDefs {
		if enum.AsEnum.Value.Name == typeDef.AsEnum.Value.Name {
			return &ModuleEnumType{
				typeDef: enum.AsEnum.Value,
				mod:     mod.res,
			}, true
		}
	}

	slog.Trace("module did not find enum", "mod", self.Name(), "enum", typeDef.AsEnum.Value.Name)
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

	cp.ObjectDefs = make([]*TypeDef, len(mod.ObjectDefs))
	for i, def := range mod.ObjectDefs {
		cp.ObjectDefs[i] = def.Clone()
	}

	cp.InterfaceDefs = make([]*TypeDef, len(mod.InterfaceDefs))
	for i, def := range mod.InterfaceDefs {
		cp.InterfaceDefs[i] = def.Clone()
	}

	cp.EnumDefs = make([]*TypeDef, len(mod.EnumDefs))
	for i, def := range mod.EnumDefs {
		cp.EnumDefs[i] = def.Clone()
	}

	if cp.SDKConfig != nil {
		cp.SDKConfig = cp.SDKConfig.Clone()
	}

	if cp.Toolchains != nil {
		cp.Toolchains = cp.Toolchains.Clone(&cp)
	}

	return &cp
}

func (mod Module) CloneWithoutDefs() *Module {
	cp := mod.Clone()

	cp.EnumDefs = []*TypeDef{}
	cp.ObjectDefs = []*TypeDef{}
	cp.InterfaceDefs = []*TypeDef{}

	return cp
}

func (mod *Module) WithDescription(desc string) *Module {
	mod = mod.Clone()
	mod.Description = strings.TrimSpace(desc)
	return mod
}

func (mod *Module) WithObject(ctx context.Context, def *TypeDef) (*Module, error) {
	mod = mod.Clone()

	if !def.AsObject.Valid {
		return nil, fmt.Errorf("expected object type def, got %s: %+v", def.Kind, def)
	}

	// skip validation+namespacing for module objects being constructed by SDK with* calls
	// they will be validated when merged into the real final module

	if mod.Deps != nil {
		if err := mod.validateTypeDef(ctx, def); err != nil {
			return nil, fmt.Errorf("failed to validate type def: %w", err)
		}
	}
	if mod.NameField != "" {
		def = def.Clone()
		modPath := mod.modulePath()
		if err := mod.namespaceTypeDef(ctx, modPath, def); err != nil {
			return nil, fmt.Errorf("failed to namespace type def: %w", err)
		}
	}

	mod.ObjectDefs = append(mod.ObjectDefs, def)
	return mod, nil
}

func (mod *Module) WithInterface(ctx context.Context, def *TypeDef) (*Module, error) {
	mod = mod.Clone()
	if !def.AsInterface.Valid {
		return nil, fmt.Errorf("expected interface type def, got %s: %+v", def.Kind, def)
	}

	// skip validation+namespacing for module objects being constructed by SDK with* calls
	// they will be validated when merged into the real final module

	if mod.Deps != nil {
		if err := mod.validateTypeDef(ctx, def); err != nil {
			return nil, fmt.Errorf("failed to validate type def: %w", err)
		}
	}
	if mod.NameField != "" {
		def = def.Clone()
		modPath := mod.modulePath()
		if err := mod.namespaceTypeDef(ctx, modPath, def); err != nil {
			return nil, fmt.Errorf("failed to namespace type def: %w", err)
		}
	}

	mod.InterfaceDefs = append(mod.InterfaceDefs, def)
	return mod, nil
}

func (mod *Module) WithEnum(ctx context.Context, def *TypeDef) (*Module, error) {
	mod = mod.Clone()
	if !def.AsEnum.Valid {
		return nil, fmt.Errorf("expected enum type def, got %s: %+v", def.Kind, def)
	}

	// skip validation+namespacing for module objects being constructed by SDK with* calls
	// they will be validated when merged into the real final module

	if mod.Deps != nil {
		if err := mod.validateTypeDef(ctx, def); err != nil {
			return nil, fmt.Errorf("failed to validate type def: %w", err)
		}
	}
	if mod.NameField != "" {
		def = def.Clone()
		modPath := mod.modulePath()
		if err := mod.namespaceTypeDef(ctx, modPath, def); err != nil {
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
