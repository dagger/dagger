// Runtime module for the Rust SDK.
//
// The Rust SDK is currently in a "manual dispatch" phase: user modules link
// against the bundled `dagger-sdk` crate (whose generated client lives in
// `crates/dagger-sdk/src/gen.rs`) and dispatch function calls by hand.
// Per-module codegen against the engine's introspection schema is not yet
// implemented — the engine still passes `introspectionJSON` to Codegen and
// ModuleRuntime, but this runtime ignores it. When the SDK gains procedural
// macros and module-side codegen, that schema will be plumbed into a
// `dagger-bootstrap`-style invocation.

package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"path/filepath"
	"slices"
	"text/template"

	"rust-sdk/internal/dagger"
)

const (
	RustImage = "rust:1.95-slim-trixie@sha256:275c320a57d0d8b6ab09454ab6d1660d70c745fb3cc85adbefad881b69a212cc"

	ModSourceDirPath = "/src"
	GenPath          = "sdk"
	CargoHome        = "/usr/local/cargo"
	CargoRegistryDir = CargoHome + "/registry"
	CargoGitDir      = CargoHome + "/git"

	BinaryName = "dagger-module"
)

//go:embed template/Cargo.toml
var tplCargoToml string

//go:embed template/src/main.rs
var tplMainRs string

//go:embed all:template/.gitignore
var tplGitignore string

type RustSdk struct {
	SourceDir *dagger.Directory
}

func New(
	// Directory with the Rust SDK source code.
	// +optional
	// +defaultPath="/sdk/rust"
	// +ignore=["**", "!Cargo.toml", "!Cargo.lock", "!crates/**", "!LICENSE", "!README.md"]
	sdkSourceDir *dagger.Directory,
) (*RustSdk, error) {
	if sdkSourceDir == nil {
		return nil, fmt.Errorf("sdk source directory not provided")
	}
	return &RustSdk{SourceDir: sdkSourceDir}, nil
}

// Codegen returns the generated module code that the engine should write
// back into the user's source tree. This includes the vendored SDK source
// under `sdk/`; the engine introspection schema is not yet consumed.
func (m *RustSdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.GeneratedCode, error) {
	ctr, err := m.codegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}
	return dag.GeneratedCode(ctr.Directory(ModSourceDirPath)).
		WithVCSGeneratedPaths([]string{GenPath + "/**"}).
		WithVCSIgnoredPaths([]string{GenPath, "target/", ".dagger/"}), nil
}

// ModuleRuntime returns a container with the user's module compiled and the
// resulting binary set as the entrypoint.
func (m *RustSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	ctr, err := m.codegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, err
	}
	srcPath := filepath.Join(ModSourceDirPath, subPath)
	binPath := filepath.Join(srcPath, "target", "release", BinaryName)

	return ctr.
		WithExec([]string{"cargo", "build", "--release", "--bin", BinaryName}).
		WithEntrypoint([]string{binPath}), nil
}

func (m *RustSdk) codegenBase(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	_ *dagger.File,
) (*dagger.Container, error) {
	name, err := modSource.ModuleOriginalName(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module name: %w", err)
	}

	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module source path: %w", err)
	}

	srcPath := filepath.Join(ModSourceDirPath, subPath)

	base := dag.Container().
		From(RustImage).
		WithExec([]string{
			"sh", "-c",
			"apt-get update && apt-get install -y --no-install-recommends " +
				"git pkg-config libssl-dev ca-certificates && rm -rf /var/lib/apt/lists/*",
		}).
		WithEnvVariable("CARGO_HOME", CargoHome).
		WithMountedCache(CargoRegistryDir, dag.CacheVolume("cargo-registry-"+RustImage)).
		WithMountedCache(CargoGitDir, dag.CacheVolume("cargo-git-"+RustImage))

	// Strip anything in the module's source tree that we regenerate
	// (vendored SDK + build artefacts) before mounting.
	ctxDir := modSource.ContextDirectory().
		WithoutDirectory(filepath.Join(subPath, GenPath)).
		WithoutDirectory(filepath.Join(subPath, "target"))

	ctr := base.
		WithMountedDirectory(ModSourceDirPath, ctxDir).
		WithWorkdir(srcPath)

	// Scaffold a fresh Cargo project if the user hasn't created one yet.
	entries, err := ctr.Directory(srcPath).Entries(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(entries, "Cargo.toml") {
		scaffold, err := renderTemplates(name)
		if err != nil {
			return nil, err
		}
		ctr = ctr.
			WithNewFile(filepath.Join(srcPath, "Cargo.toml"), scaffold.cargoToml).
			WithNewFile(filepath.Join(srcPath, "src", "main.rs"), scaffold.mainRs).
			WithNewFile(filepath.Join(srcPath, ".gitignore"), tplGitignore)
	}

	// Vendor the SDK source into the user's module directory so the
	// `dagger-sdk = { path = "sdk/..." }` dependency resolves both when the
	// engine builds the module (inside this container) and when the user
	// opens the module locally for editing.
	ctr = ctr.WithDirectory(filepath.Join(srcPath, GenPath), m.SourceDir)

	return ctr, nil
}

type scaffoldFiles struct {
	cargoToml string
	mainRs    string
}

func renderTemplates(moduleName string) (*scaffoldFiles, error) {
	data := struct {
		ModuleName  string
		PackageName string
		BinaryName  string
		SdkPath     string
	}{
		ModuleName:  moduleName,
		PackageName: toCrateName(moduleName),
		BinaryName:  BinaryName,
		SdkPath:     GenPath,
	}

	cargo, err := renderTemplate("Cargo.toml", tplCargoToml, data)
	if err != nil {
		return nil, err
	}
	main, err := renderTemplate("main.rs", tplMainRs, data)
	if err != nil {
		return nil, err
	}
	return &scaffoldFiles{cargoToml: cargo, mainRs: main}, nil
}

func renderTemplate(name, body string, data any) (string, error) {
	tpl, err := template.New(name).Parse(body)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute %s: %w", name, err)
	}
	return buf.String(), nil
}

// toCrateName converts a module name to a valid Cargo crate name: lowercase
// ASCII letters/digits, with everything else collapsed to underscores.
func toCrateName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
