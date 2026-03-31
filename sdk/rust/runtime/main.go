// Runtime module for the Rust SDK.
//
// This module implements the Dagger SDK interface for Rust modules.
// It handles building a container with the Rust toolchain, running codegen,
// compiling the user's module, and setting up the entrypoint for execution.

package main

import (
	"context"
	_ "embed"
	"fmt"
	"path"

	"dagger/rust-sdk/internal/dagger"
)

const (
	ModSourceDirPath   = "/src"
	DaggerSdkMountPath = "/sdk/crates/dagger-sdk"
	MacrosMountPath    = "/sdk/crates/dagger-sdk-derive"
	CodegenMountPath   = "/sdk/crates/dagger-codegen"
	BootstrapMountPath = "/sdk/crates/dagger-bootstrap"
	SchemaPath         = "/schema.json"
	GenPath            = "dagger_gen.rs"
	CargoToml          = "Cargo.toml"
	RustBaseImage      = "rust:1.87-slim-bookworm"
)

//go:embed template/Cargo.toml
var tplCargoToml string

//go:embed template/src/main.rs
var tplMain string


// Functions for building the runtime module for the Rust SDK.
//
// The server interacts directly with the ModuleRuntime and Codegen functions.
type RustSdk struct {
	// +private
	Container *dagger.Container

	// +private
	ModSource *dagger.ModuleSource

	// +private
	ContextDir *dagger.Directory

	// +private
	SubPath string

	// +private
	ModName string

	// +private
	ContextDirPath string

	// +private
	IsInit bool

	// +private
	DaggerSdkSourceDir *dagger.Directory

	// +private
	MacrosSourceDir *dagger.Directory

	// +private
	CodegenSourceDir *dagger.Directory

	// +private
	BootstrapSourceDir *dagger.Directory

	// +private
	WorkspaceCargoToml *dagger.File
}

func New(
	// The workspace Cargo.toml that ties all SDK crates together.
	// +defaultPath="../Cargo.toml"
	workspaceCargoToml *dagger.File,
	// Directory with the dagger-sdk source code.
	// +defaultPath="../crates/dagger-sdk"
	// +ignore=["target"]
	daggerSdkSourceDir *dagger.Directory,
	// Directory with the Rust module macros source code.
	// +defaultPath="../crates/dagger-sdk-derive"
	// +ignore=["target"]
	macrosSourceDir *dagger.Directory,
	// Directory with the codegen library source code.
	// +defaultPath="../crates/dagger-codegen"
	// +ignore=["target"]
	codegenSourceDir *dagger.Directory,
	// Directory with the bootstrap CLI source code.
	// +defaultPath="../crates/dagger-bootstrap"
	// +ignore=["target"]
	bootstrapSourceDir *dagger.Directory,
) *RustSdk {
	return &RustSdk{
		Container:          dag.Container(),
		WorkspaceCargoToml: workspaceCargoToml,
		DaggerSdkSourceDir: daggerSdkSourceDir,
		MacrosSourceDir:    macrosSourceDir,
		CodegenSourceDir:   codegenSourceDir,
		BootstrapSourceDir: bootstrapSourceDir,
	}
}

// Generated code for the Rust module.
func (m *RustSdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.GeneratedCode, error) {
	m, err := m.common(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	// Run codegen to generate typed bindings from introspection JSON
	m.withCodegen(introspectionJSON)

	return dag.
		GeneratedCode(m.Container.Directory(m.ContextDirPath)).
		WithVCSGeneratedPaths([]string{path.Join(m.SubPath, GenPath)}).
		WithVCSIgnoredPaths([]string{"target"}), nil
}

// Container for executing the Rust module runtime.
func (m *RustSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	m, err := m.common(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	// Run codegen before building
	m.withCodegen(introspectionJSON)

	srcPath := path.Join(m.ContextDirPath, m.SubPath)

	// Build the module binary
	ctr := m.Container.
		WithExec([]string{"cargo", "build", "--release"}).
		WithEntrypoint([]string{path.Join(srcPath, "target/release/dagger-module")})

	return ctr, nil
}

// common steps for the ModuleRuntime and Codegen functions.
func (m *RustSdk) common(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*RustSdk, error) {
	if err := m.load(ctx, modSource); err != nil {
		return nil, err
	}
	m.withBase()
	m.withTemplate()
	m.withSource()
	return m, nil
}

// withCodegen runs dagger-bootstrap to generate dagger_gen.rs from introspection JSON.
func (m *RustSdk) withCodegen(introspectionJSON *dagger.File) {
	if introspectionJSON == nil {
		return
	}

	m.Container = m.Container.
		WithMountedFile(SchemaPath, introspectionJSON).
		// Build dagger-bootstrap from source within the SDK workspace
		WithWorkdir("/sdk").
		WithExec([]string{"cargo", "build", "--release", "-p", "dagger-bootstrap"}).
		// Switch back to module source dir and run codegen
		WithWorkdir(path.Join(m.ContextDirPath, m.SubPath)).
		WithExec([]string{
			"/sdk/target/release/dagger-bootstrap",
			"generate", SchemaPath, "--output", GenPath,
		}).
		// Rewrite imports: the generated code uses crate:: paths but we need dagger_sdk::module::gen_deps::
		WithExec([]string{"sed", "-i",
			"-e", "s|use crate::core::cli_session::DaggerSessionProc|use dagger_sdk::module::gen_deps::DaggerSessionProc|g",
			"-e", "s|use crate::core::graphql_client::DynGraphQLClient|use dagger_sdk::module::gen_deps::DynGraphQLClient|g",
			"-e", "s|use crate::errors::DaggerError|use dagger_sdk::module::gen_deps::DaggerError|g",
			"-e", "s|use crate::id::IntoID|use dagger_sdk::module::gen_deps::IntoID|g",
			"-e", "s|use crate::querybuilder::Selection|use dagger_sdk::module::gen_deps::Selection|g",
			GenPath,
		})
}

// load gathers metadata from the module source.
func (m *RustSdk) load(ctx context.Context, modSource *dagger.ModuleSource) error {
	m.ModSource = modSource
	m.ContextDir = modSource.ContextDirectory()

	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return fmt.Errorf("failed to get source subpath: %w", err)
	}
	m.SubPath = subPath

	modName, err := modSource.ModuleOriginalName(ctx)
	if err != nil {
		return fmt.Errorf("failed to get module name: %w", err)
	}
	m.ModName = modName

	m.ContextDirPath = ModSourceDirPath

	// Check if this is a new module (no Cargo.toml yet)
	srcDir := m.ContextDir.Directory(m.SubPath)
	entries, err := srcDir.Entries(ctx)
	if err != nil {
		m.IsInit = true
		return nil
	}

	hasCargoToml := false
	for _, e := range entries {
		if e == CargoToml {
			hasCargoToml = true
			break
		}
	}
	m.IsInit = !hasCargoToml

	return nil
}

// withBase sets up the base Rust container.
func (m *RustSdk) withBase() {
	m.Container = dag.Container().
		From(RustBaseImage).
		WithExec([]string{"apt-get", "update", "-y"}).
		WithExec([]string{"apt-get", "install", "-y", "pkg-config", "libssl-dev"}).
		WithMountedCache("/usr/local/cargo/registry", dag.CacheVolume("rust-cargo-registry")).
		WithMountedCache("/usr/local/cargo/git", dag.CacheVolume("rust-cargo-git")).
		WithEnvVariable("CARGO_HOME", "/usr/local/cargo").
		// Mount all SDK crates
		WithMountedDirectory(DaggerSdkMountPath, m.DaggerSdkSourceDir).
		WithMountedDirectory(MacrosMountPath, m.MacrosSourceDir).
		// Mount codegen crates
		WithMountedDirectory(CodegenMountPath, m.CodegenSourceDir).
		WithMountedDirectory(BootstrapMountPath, m.BootstrapSourceDir).
		// Mount the workspace Cargo.toml so workspace references resolve
		WithMountedFile("/sdk/Cargo.toml", m.WorkspaceCargoToml).
		// Cache the SDK workspace build target
		WithMountedCache("/sdk/target", dag.CacheVolume("rust-sdk-target"))
}

// withTemplate generates template files for new modules.
func (m *RustSdk) withTemplate() {
	if !m.IsInit {
		return
	}

	cargoToml := fmt.Sprintf(tplCargoToml, m.ModName)
	m.ContextDir = m.ContextDir.
		WithNewFile(path.Join(m.SubPath, "Cargo.toml"), cargoToml).
		WithNewFile(path.Join(m.SubPath, "src/main.rs"), tplMain)
}

// withSource mounts the module source into the container.
func (m *RustSdk) withSource() {
	srcPath := path.Join(m.ContextDirPath, m.SubPath)

	m.Container = m.Container.
		WithWorkdir(srcPath).
		WithMountedDirectory(m.ContextDirPath, m.ContextDir).
		WithEnvVariable("DAGGER_MODULE", m.ModName)
}
