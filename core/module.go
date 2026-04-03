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
	ObjectDefs dagql.ObjectResultArray[*TypeDef] `field:"true" name:"objects" doc:"Objects served by this module."`

	// The module's interfaces
	InterfaceDefs dagql.ObjectResultArray[*TypeDef] `field:"true" name:"interfaces" doc:"Interfaces served by this module."`

	// The module's enumerations
	EnumDefs dagql.ObjectResultArray[*TypeDef] `field:"true" name:"enums" doc:"Enumerations served by this module."`

	// IsToolchain indicates this module was loaded as a toolchain dependency.
	// Toolchain modules are allowed to share types with the modules that depend on them.
	IsToolchain bool

	// Toolchains manages all toolchain modules and their configuration.
	Toolchains *ToolchainRegistry

	persistedResultID uint64
	IncludeSelfInDeps bool

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
var _ dagql.HasDependencyResults = (*Module)(nil)

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
		for i, dep := range mod.Deps.mods {
			depInst := dep.ModuleResult()
			if depInst.Self() == nil {
				continue
			}
			depID, err := depInst.ID()
			if err != nil {
				return nil, fmt.Errorf("attach module self dependency %q: dep ID: %w", dep.Name(), err)
			}
			if depID == nil || depID.EngineResultID() != attachedSelfID.EngineResultID() {
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
	ObjectDefResultIDs            []uint64                        `json:"objectDefResultIDs,omitempty"`
	InterfaceDefResultIDs         []uint64                        `json:"interfaceDefResultIDs,omitempty"`
	EnumDefResultIDs              []uint64                        `json:"enumDefResultIDs,omitempty"`
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

	persisted.IncludeSelfInDeps = mod.IncludeSelfInDeps
	if mod.Deps != nil {
		persisted.DepModuleResultIDs = make([]uint64, 0, len(mod.Deps.Mods()))
		for _, dep := range mod.Deps.Mods() {
			depInst := dep.ModuleResult()
			if depInst.Self() == nil {
				continue
			}
			if mod.IncludeSelfInDeps && depInst.Self() == mod {
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
	persisted.IsToolchain = mod.IsToolchain
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

	loadedDepMods := make(map[uint64]dagql.ObjectResult[*Module], len(persisted.DepModuleResultIDs))
	for _, depID := range persisted.DepModuleResultIDs {
		depRes, err := loadPersistedObjectResultByResultID[*Module](ctx, dag, depID, "module dependency")
		if err != nil {
			return nil, err
		}
		deps = deps.Append(NewUserMod(depRes))
		loadedDepMods[depID] = depRes
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
		IsToolchain:                   persisted.IsToolchain,
		IncludeSelfInDeps:             persisted.IncludeSelfInDeps,
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

//nolint:gocyclo
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

func (mod *userMod) Install(ctx context.Context, dag *dagql.Server) error {
	return mod.install(ctx, dag)
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

func (mod *userMod) install(ctx context.Context, dag *dagql.Server) error {
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
		if err := obj.Install(ctx, dag); err != nil {
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

	if cp.Toolchains != nil {
		cp.Toolchains = cp.Toolchains.Clone(&cp)
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
