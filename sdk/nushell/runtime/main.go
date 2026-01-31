// Runtime module for Dagger Nushell SDK
//
// This module provides the runtime environment for executing Nushell-based
// Dagger modules. It implements the ModuleRuntime and Codegen functions
// required by Dagger to discover and execute functions in Nushell modules.

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"nushell-sdk/internal/dagger"
)

// NushellSdk provides the runtime for Nushell-based Dagger modules
type NushellSdk struct {
	// Container is the active container instance
	Container *dagger.Container

	// ModSource contains the module source information
	ModSource *dagger.ModuleSource

	// ContextDir is the module's context directory
	ContextDir *dagger.Directory

	// ContextDirPath is the filesystem path to the context directory
	ContextDirPath string

	// SubPath is the subpath within the context directory
	SubPath string

	// ModName is the module name
	ModName string

	// Debug enables debug mode with terminal access
	Debug bool
}

// FunctionParameter represents a parameter in a Nushell function
type FunctionParameter struct {
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	Description  string  `json:"description"`
	DefaultValue *string `json:"default_value"` // Pointer to distinguish between null and empty string
}

// FunctionDefinition represents a discovered Nushell function
type FunctionDefinition struct {
	Name       string              `json:"name"`
	Parameters []FunctionParameter `json:"parameters"`
	ReturnType string              `json:"return_type"`
	IsCheck    bool                `json:"is_check"`
}

const (
	// Base Nushell image to use for runtime (official GHCR image)
	nushellImage = "ghcr.io/nushell/nushell:latest"

	// Path where module source will be mounted
	moduleSourcePath = "/src"

	// Path to the runtime entrypoint
	runtimePath = "/runtime"
)

// Template for main.nu - embedded at compile time
//
//go:embed templates/main.nu
var tplMainNu string

// Helper method to add a new file with contents to the module's source
func (m *NushellSdk) AddNewFile(name, contents string) {
	if m.SubPath != "" {
		name = m.SubPath + "/" + name
	}
	m.ContextDir = m.ContextDir.WithNewFile(name, contents)
}

// ModuleRuntime returns a container configured to execute the Nushell module
//
// This is the primary function called by Dagger to get an execution environment
// for user modules. It sets up a container with Nushell, mounts the user's code,
// and configures the connection back to the Dagger Engine.
func (m *NushellSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	// Load module metadata and set up base environment
	sdk, err := m.Common(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	return sdk.Container, nil
}

// ModuleTypes discovers and returns the types defined in the Nushell module
//
// This function parses the user's Nushell code to extract exported functions
// and their signatures, then builds a Dagger Module with appropriate type
// definitions. The runtime.nu Nushell script handles the actual parsing.
func (m *NushellSdk) ModuleTypes(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
	outputFilePath string,
) (*dagger.Container, error) {
	// Load module metadata and set up base environment
	sdk, err := m.Common(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	// Execute the runtime.nu script to parse user module and extract function definitions
	parserOutput, err := sdk.Container.
		WithExec([]string{runtimePath, "--register"}, dagger.ContainerWithExecOpts{
			UseEntrypoint: false,
		}).
		Stdout(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed to parse Nushell module: %w", err)
	}

	// Parse the JSON output from runtime.nu
	var functions []FunctionDefinition
	if err := json.Unmarshal([]byte(parserOutput), &functions); err != nil {
		return nil, fmt.Errorf("failed to parse function definitions: %w", err)
	}

	// Create a Module with the discovered functions
	modName, err := modSource.ModuleName(ctx)
	if err != nil {
		return nil, err
	}

	mod := dag.Module()

	// For each discovered function, create a Dagger function definition
	if len(functions) > 0 {
		// Create the main object for this module
		mainObject := dag.TypeDef().WithObject(modName)

		// Add each function to the main object
		for _, fn := range functions {
			// Convert return type string to TypeDef
			returnType, err := typeStringToTypeDef(fn.ReturnType)
			if err != nil {
				return nil, fmt.Errorf("failed to convert return type for %s: %w", fn.Name, err)
			}

			funcDef := dag.Function(fn.Name, returnType)

			// Add parameters
			for _, param := range fn.Parameters {
				paramType, err := typeStringToTypeDef(param.Type)
				if err != nil {
					return nil, fmt.Errorf("failed to convert parameter type for %s.%s: %w", fn.Name, param.Name, err)
				}

				opts := dagger.FunctionWithArgOpts{
					Description: param.Description,
				}

				// If parameter has a default value, make it optional
				if param.DefaultValue != nil {
					opts.DefaultValue = dagger.JSON(*param.DefaultValue)
				}

				funcDef = funcDef.WithArg(param.Name, paramType, opts)
			}

			// If this is a check function, mark it as such
			if fn.IsCheck {
				funcDef = funcDef.WithCheck()
			}

			mainObject = mainObject.WithFunction(funcDef)
		}

		mod = mod.WithObject(mainObject)
	}

	// Get the Module ID
	modID, err := mod.ID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module ID: %w", err)
	}

	// JSON-encode the Module ID (it should be marshaled as a JSON string)
	modIDJSON, err := json.Marshal(string(modID))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal module ID: %w", err)
	}

	// Create a container that writes the Module ID to the output file
	ctr := sdk.Container.
		WithNewFile(outputFilePath, string(modIDJSON))

	return ctr, nil
}

// ModuleTypesExp is an alias for ModuleTypes for compatibility
func (m *NushellSdk) ModuleTypesExp(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
	outputFilePath string,
) (*dagger.Container, error) {
	return m.ModuleTypes(ctx, modSource, introspectionJSON, outputFilePath)
}

// Codegen generates code for the module (creates initial template)
//
// During initialization, this function creates the template main.nu file
// For existing modules, it can generate Nushell bindings for Dagger types
func (m *NushellSdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File, //nolint:unparam // Required by SDK interface, may be used in future
) (*dagger.GeneratedCode, error) {
	// Get context directory
	contextDir := modSource.ContextDirectory()

	// Get source subpath
	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, err
	}

	// Check if main.nu already exists
	entries, err := contextDir.Entries(ctx)
	if err != nil {
		return nil, err
	}

	hasMainNu := false
	for _, entry := range entries {
		if entry == "main.nu" {
			hasMainNu = true
			break
		}
	}

	// If main.nu doesn't exist, create it from embedded template
	if !hasMainNu {
		filename := "main.nu"
		if subPath != "" {
			filename = subPath + "/main.nu"
		}
		contextDir = contextDir.WithNewFile(filename, tplMainNu)
	}

	// Return generated code with the context directory
	return dag.GeneratedCode(contextDir).
		WithVCSIgnoredPaths([]string{".dagger"}), nil
}

// Common performs the shared setup steps for both ModuleRuntime and Codegen
//
// This function orchestrates the setup pipeline:
// 1. Load module metadata
// 2. Create base Nushell container
// 3. Mount user module source
// 4. Set up Dagger Engine connection
func (m *NushellSdk) Common(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*NushellSdk, error) {
	// Create a new SDK instance with module metadata
	sdk := &NushellSdk{
		ModSource: modSource,
	}

	// Load module information
	sdk, err := sdk.Load(ctx)
	if err != nil {
		return nil, err
	}

	// Build the runtime container
	sdk = sdk.WithBase().
		WithSDK(introspectionJSON).
		WithSource()

	return sdk, nil
}

// Load extracts module metadata and configuration
func (m *NushellSdk) Load(ctx context.Context) (*NushellSdk, error) {
	// Get module name
	modName, err := m.ModSource.ModuleName(ctx)
	if err != nil {
		return nil, err
	}
	m.ModName = modName

	// Get context directory
	contextDir, err := m.ModSource.ContextDirectory().Sync(ctx)
	if err != nil {
		return nil, err
	}
	m.ContextDir = contextDir

	// Get source subpath
	subPath, err := m.ModSource.SourceSubpath(ctx)
	if err != nil {
		return nil, err
	}
	m.SubPath = subPath

	return m, nil
}

// WithBase creates the base Nushell container
func (m *NushellSdk) WithBase() *NushellSdk {
	// Build the executor helper
	executor := dag.Container().
		From("golang:1.24-alpine").
		WithWorkdir("/build").
		WithFile("/build/executor.go", dag.CurrentModule().Source().File("runtime/executor.go")).
		WithExec([]string{"go", "mod", "init", "executor"}).
		WithExec([]string{"go", "get", "dagger.io/dagger@latest"}).
		WithExec([]string{"go", "build", "-o", "executor", "executor.go"}).
		File("/build/executor")

	m.Container = dag.Container().
		From(nushellImage).
		// Run as root to avoid permission issues
		WithUser("root").
		// Disable output buffering for better streaming
		WithEnvVariable("NO_COLOR", "1").
		// Set working directory
		WithWorkdir(moduleSourcePath).
		// Mount the runtime entrypoint
		WithFile(
			runtimePath,
			dag.CurrentModule().Source().File("runtime/runtime.nu"),
			dagger.ContainerWithFileOpts{Permissions: 0o755},
		).
		// Mount the modular dag directory with all API operations and wrappers
		WithDirectory(
			"/usr/local/lib/dag",
			dag.CurrentModule().Source().Directory("runtime/dag"),
		).
		// Create a dag.nu entry point that re-exports from the modular structure
		WithNewFile(
			"/usr/local/lib/dag.nu",
			"#!/usr/bin/env nu\n"+
				"# Dagger API entry point - re-exports from modular structure\n"+
				"export use dag/mod.nu *\n",
			dagger.ContainerWithNewFileOpts{Permissions: 0o644},
		).
		// Mount the executor helper
		WithFile(
			"/usr/local/bin/executor",
			executor,
			dagger.ContainerWithFileOpts{Permissions: 0o755},
		).
		// Set the entrypoint to the executor for function calls
		WithEntrypoint([]string{"/usr/local/bin/executor"})

	return m
}

// WithSDK installs the Dagger SDK and sets up the introspection schema
func (m *NushellSdk) WithSDK(introspectionJSON *dagger.File) *NushellSdk {
	// For MVP, we'll mount the introspection JSON for future use
	// Later, this will be used to generate Nushell bindings
	m.Container = m.Container.
		WithMountedFile("/schema.json", introspectionJSON)

	return m
}

// WithSource mounts the user's module source code
func (m *NushellSdk) WithSource() *NushellSdk {
	// Mount the module source at the expected path
	// If there's a subpath, we need to get the directory at that subpath
	sourceDir := m.ContextDir
	if m.SubPath != "" {
		sourceDir = m.ContextDir.Directory(m.SubPath)
	}

	m.Container = m.Container.
		WithMountedDirectory(moduleSourcePath, sourceDir, dagger.ContainerWithMountedDirectoryOpts{
			Owner: "root", // Ensure we have read permissions
		}).
		WithWorkdir(moduleSourcePath)

	// Set environment variables for module metadata
	if m.ModName != "" {
		m.Container = m.Container.
			WithEnvVariable("DAGGER_MODULE_NAME", m.ModName)
	}

	return m
}

// typeStringToTypeDef converts a Nushell type string to a Dagger TypeDef
// For Nushell, we use 'record' as the type for Dagger objects, and infer the actual
// Dagger type from the parameter name
func typeStringToTypeDef(typeStr string) (*dagger.TypeDef, error) {
	switch typeStr {
	case "string":
		return dag.TypeDef().WithKind(dagger.TypeDefKindString), nil
	case "int":
		return dag.TypeDef().WithKind(dagger.TypeDefKindInteger), nil
	case "bool", "boolean":
		return dag.TypeDef().WithKind(dagger.TypeDefKindBoolean), nil
	case "record":
		// Records are used for Dagger objects in Nushell
		// The actual type should be inferred from context (parameter name)
		// For now, default to Container, but this should be improved
		// TODO: Pass parameter name to this function for better inference
		return dag.TypeDef().WithObject("Container"), nil
	case "Container":
		// Container is an object type in Dagger
		return dag.TypeDef().WithObject("Container"), nil
	case "Directory":
		// Directory is an object type in Dagger
		return dag.TypeDef().WithObject("Directory"), nil
	case "File":
		// File is an object type in Dagger
		return dag.TypeDef().WithObject("File"), nil
	case "Secret":
		// Secret is an object type in Dagger
		return dag.TypeDef().WithObject("Secret"), nil
	case "Service":
		// Service is an object type in Dagger
		return dag.TypeDef().WithObject("Service"), nil
	case "CacheVolume":
		// CacheVolume is an object type in Dagger
		return dag.TypeDef().WithObject("CacheVolume"), nil
	case "any":
		// Use string as default for "any" type
		return dag.TypeDef().WithKind(dagger.TypeDefKindString), nil
	default:
		// Check if it's a list type: list<type> or list
		if strings.HasPrefix(typeStr, "list<") && strings.HasSuffix(typeStr, ">") {
			// Extract element type from list<type>
			elementTypeStr := typeStr[5 : len(typeStr)-1]
			elementType, err := typeStringToTypeDef(elementTypeStr)
			if err != nil {
				return nil, fmt.Errorf("invalid list element type: %w", err)
			}
			return dag.TypeDef().WithListOf(elementType), nil
		} else if typeStr == "list" {
			// Untyped list, default to list<string>
			elementType := dag.TypeDef().WithKind(dagger.TypeDefKindString)
			return dag.TypeDef().WithListOf(elementType), nil
		}

		return nil, fmt.Errorf("unsupported type: %s", typeStr)
	}
}
