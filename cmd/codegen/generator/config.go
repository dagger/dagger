package generator

import "dagger.io/dagger"

type Config struct {
	// Lang is the language to generate the module for.
	Lang SDKLang

	// OutputDir is the path to put the generated code.
	// Usually this is the path to the module source directory.
	// This allows generating extra file aside the client bindings
	// like go.mod, go.sum etc...
	OutputDir string

	// IntrospectionJSON is an optional pre-computed introspection json string.
	IntrospectionJSON string

	// A dagger client connected to the engine running the codegen.
	// This may be nil if the codegen is run outside of a dagger context and should
	// only be set if introspectionJSON or moduleSourceID are set.
	Dag *dagger.Client

	// Generate the client in bundle mode.
	Bundle bool

	// ModuleConfig is the specific config to generate module or typedefs.
	ModuleConfig *ModuleGeneratorConfig

	// ClientConfig is the specific config to generate standalone client.
	ClientConfig *ClientGeneratorConfig
}

// Close existing dagger client if it exists.
// This is a convenience method to be used in the main codegen command using a defer.
func (c *Config) Close() error {
	if c.Dag != nil {
		return c.Dag.Close()
	}

	return nil
}

// Specific configuration for module generation.
type ModuleGeneratorConfig struct {
	// Name of the module to generate code for.
	ModuleName string

	// ModuleSourcePath is the subpath in OutputDir where the module source subpath is located.
	ModuleSourcePath string

	// ModuleParentPath is the path from the module source subpath to the context directory
	ModuleParentPath string

	// Whether we are initializing a new module.
	// Currently, this is only used in go codegen to enforce backwards-compatible behavior
	// where a pre-existing go.mod file is checked during dagger init for whether its module
	// name is the expected value.
	IsInit bool
}

type ModuleSourceDependency struct {
	Kind   string
	Name   string `json:"moduleOriginalName"`
	Pin    string
	Source string `json:"asString"`
}

// Specific configuration for client generation.
type ClientGeneratorConfig struct {
	// The list of all dependencies used by the module.
	// This is used by the client generator to automatically serves the
	// dependencies when connecting to the client.
	ModuleDependencies []ModuleSourceDependency
}
