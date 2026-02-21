package core

import (
	"context"
	"fmt"
	"maps"
	"sort"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

// ToolchainRegistry manages toolchain modules for a parent module.
// It consolidates all toolchain-related state and logic into a single,
// testable abstraction, replacing the scattered maps and special-case
// checks throughout the codebase.
type ToolchainRegistry struct {
	entries map[string]*ToolchainEntry
	parent  *Module
}

// ToolchainEntry represents a single toolchain module with its configuration.
type ToolchainEntry struct {
	Module           *Module
	FieldName        string
	ArgumentConfigs  []*modules.ModuleConfigArgument
	IgnoreChecks     []string
	IgnoreGenerators []string
}

// NewToolchainRegistry creates a new registry for the given parent module.
func NewToolchainRegistry(parent *Module) *ToolchainRegistry {
	return &ToolchainRegistry{
		entries: make(map[string]*ToolchainEntry),
		parent:  parent,
	}
}

func (r *ToolchainRegistry) Clone(clonedParent *Module) *ToolchainRegistry {
	return &ToolchainRegistry{
		entries: maps.Clone(r.entries),
		parent:  clonedParent,
	}
}

// Register adds a toolchain to the registry.
// originalName is the toolchain's original name (with hyphens)
// fieldName is the camelCase GraphQL field name
func (r *ToolchainRegistry) Register(originalName, fieldName string, mod *Module) {
	r.entries[originalName] = &ToolchainEntry{
		Module:    mod,
		FieldName: fieldName,
	}
}

// Get retrieves a toolchain entry by its original name.
func (r *ToolchainRegistry) Get(originalName string) (*ToolchainEntry, bool) {
	entry, ok := r.entries[originalName]
	return entry, ok
}

// GetByFieldName retrieves a toolchain entry by its GraphQL field name.
func (r *ToolchainRegistry) GetByFieldName(fieldName string) (*ToolchainEntry, bool) {
	for _, entry := range r.entries {
		if entry.FieldName == fieldName {
			return entry, true
		}
	}
	return nil, false
}

// Entries returns all toolchain entries in the registry.
func (r *ToolchainRegistry) Entries() []*ToolchainEntry {
	entries := make([]*ToolchainEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		entries = append(entries, entry)
	}
	return entries
}

// CreateProxyField creates a dagql.Field that proxies calls to a toolchain module.
// This consolidates the toolchainProxyFunction logic from object.go.
func (entry *ToolchainEntry) CreateProxyField(ctx context.Context, parentMod *Module, fun *Function, dag *dagql.Server) (dagql.Field[*ModuleObject], error) {
	tcMod := entry.Module

	// Find the toolchain's main object type
	if len(tcMod.ObjectDefs) == 0 {
		return dagql.Field[*ModuleObject]{}, fmt.Errorf("toolchain module %q has no objects", tcMod.Name())
	}

	var mainObjDef *ObjectTypeDef
	for _, objDef := range tcMod.ObjectDefs {
		if objDef.AsObject.Valid && gqlObjectName(objDef.AsObject.Value.OriginalName) == gqlObjectName(tcMod.OriginalName) {
			mainObjDef = objDef.AsObject.Value
			break
		}
	}
	if mainObjDef == nil {
		return dagql.Field[*ModuleObject]{}, fmt.Errorf("toolchain module %q has no main object", tcMod.Name())
	}

	// Check if toolchain has a constructor
	hasConstructor := mainObjDef.Constructor.Valid

	if !hasConstructor {
		// No constructor - treat as a zero-argument function that returns an uninitialized object
		spec, err := fun.FieldSpec(ctx, parentMod)
		if err != nil {
			return dagql.Field[*ModuleObject]{}, fmt.Errorf("failed to get field spec for toolchain: %w", err)
		}
		spec.Module = parentMod.IDModule(ctx)

		return dagql.Field[*ModuleObject]{
			Spec: &spec,
			Func: func(ctx context.Context, obj dagql.ObjectResult[*ModuleObject], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
				// Return an instance of the toolchain's main object with empty fields
				// The toolchain module's own resolvers will handle function calls on this object
				return dagql.NewResultForCurrentID(ctx, &ModuleObject{
					Module:  tcMod,
					TypeDef: mainObjDef,
					Fields:  map[string]any{}, // empty fields, functions will be called on the toolchain's runtime
				})
			},
		}, nil
	}

	// Has constructor - create a ModFunction for it and use its spec
	constructor := mainObjDef.Constructor.Value

	modFun, err := NewModFunction(
		ctx,
		tcMod,
		mainObjDef,
		constructor,
	)
	if err != nil {
		return dagql.Field[*ModuleObject]{}, fmt.Errorf("failed to create toolchain constructor function %q: %w", fun.Name, err)
	}

	// Apply local user defaults
	if err := modFun.mergeUserDefaultsTypeDefs(ctx); err != nil {
		return dagql.Field[*ModuleObject]{}, fmt.Errorf("failed to merge user defaults for toolchain constructor %q: %w", fun.Name, err)
	}

	// Get the constructor's spec, which includes its arguments
	spec, err := constructor.FieldSpec(ctx, tcMod)
	if err != nil {
		return dagql.Field[*ModuleObject]{}, fmt.Errorf("failed to get field spec for toolchain constructor: %w", err)
	}
	// But use the toolchain name from the parent module
	spec.Name = fun.Name
	spec.Module = parentMod.IDModule(ctx)
	spec.GetCacheConfig = modFun.CacheConfigForCall

	return dagql.Field[*ModuleObject]{
		Spec: &spec,
		Func: func(ctx context.Context, obj dagql.ObjectResult[*ModuleObject], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
			opts := &CallOpts{
				ParentTyped:    obj,
				ParentFields:   nil,
				SkipSelfSchema: false,
				Server:         dag,
			}
			for name, val := range args {
				opts.Inputs = append(opts.Inputs, CallInput{
					Name:  name,
					Value: val,
				})
			}
			// NB: ensure deterministic order
			sort.Slice(opts.Inputs, func(i, j int) bool {
				return opts.Inputs[i].Name < opts.Inputs[j].Name
			})
			return modFun.Call(ctx, opts)
		},
	}, nil
}
