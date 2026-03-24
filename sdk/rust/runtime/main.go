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
	DaggerSdkMountPath = "/sdk/dagger-sdk"
	MacrosMountPath    = "/sdk/dagger-sdk-derive"
	CodegenMountPath   = "/sdk/dagger-codegen"
	BootstrapMountPath = "/sdk/dagger-bootstrap"
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
}

func New(
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

// sdkWorkspaceToml is a workspace Cargo.toml that ties together all the SDK crates
// mounted at /sdk/. This is needed because the individual crates use workspace
// references (version.workspace, edition.workspace, etc.).
const sdkWorkspaceToml = `[workspace]
members = ["dagger-sdk", "dagger-codegen", "dagger-bootstrap", "dagger-sdk-derive"]
resolver = "2"

[workspace.package]
version = "0.20.3"
edition = "2021"
authors = ["kjuulh <contact@kasperhermansen.com>", "Dagger <hello@dagger.io>"]
repository = "https://github.com/dagger/dagger"

[workspace.dependencies]
dagger-codegen = { path = "dagger-codegen" }
dagger-bootstrap = { path = "dagger-bootstrap" }
dagger-sdk = { path = "dagger-sdk", default-features = false }
dagger-sdk-derive = { path = "dagger-sdk-derive" }

eyre = "0.6.9"
color-eyre = "0.6.2"
serde = { version = "1.0.195", features = ["derive"] }
serde_json = "1.0.111"
serde_graphql_input = { version = "0.1.1" }
tokio = { version = "1.35.1", features = ["full"] }
tracing = { version = "0.1.40", features = ["log"] }
tracing-subscriber = { version = "0.3.18", features = ["tracing-log", "tracing"] }
thiserror = "1.0.56"
futures = "0.3.30"
derive_builder = "0.12.0"
base64 = "0.21.6"
dirs = "5.0.1"
flate2 = { version = "1.0.28", features = ["rust_backend"] }
graphql_client = { version = "0.13.0", features = ["reqwest-rustls", "graphql_query_derive"], default-features = false }
hex = "0.4.3"
hex-literal = "0.4.1"
platform-info = "2.0.2"
reqwest = { version = "0.11.23", features = ["stream", "rustls-tls"], default-features = false }
sha2 = "0.10.8"
tar = "0.4.40"
tempfile = "3.9.0"
async-trait = "0.1.77"
genco = "0.17.8"
convert_case = "0.6.0"
itertools = "0.12.0"
clap = "4.4.14"

pretty_assertions = "1.4.0"
rand = "0.8.5"
tracing-test = "0.2.4"
`

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
		// Create a workspace Cargo.toml so workspace references resolve
		WithNewFile("/sdk/Cargo.toml", sdkWorkspaceToml).
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
