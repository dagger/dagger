// Runtime module for the Python SDK.

package main

import (
	"context"
	_ "embed"
	"fmt"
	"path"
	"strings"

	"github.com/iancoleman/strcase"
)

const (
	DefaultVersion        = "3.11"
	DefaultImage          = "python:%s-slim"
	DefaultDigest         = "sha256:ce81dc539f0aedc9114cae640f8352fad83d37461c24a3615b01f081d0c0583a"
	ModSourceDirPath      = "/src"
	RuntimeExecutablePath = "/runtime"
	GenDir                = "sdk"
	GenPath               = "src/dagger/client/gen.py"
	SchemaPath            = "/schema.json"
	VenvPath              = "/opt/venv"
	LockFilePath          = "requirements.lock"
	MainFilePath          = "src/main/__init__.py"
	MainObjectName        = "Main"
)

// UserConfig is the custom user configuration that users can add to their pyproject.toml.
//
// For example:
// ```toml
// [tool.dagger]
// use-uv = false
// ```
type UserConfig struct {
	// UseUv is for choosing the faster uv tool instead of pip to install packages.
	UseUv bool `toml:"use-uv"`

	// UvVersion is the version of the uv tool to use.
	//
	// By default, it's pinned to a specific version in each dagger version,
	// can be useful to get a newer version to fix a bug or get a new feature.
	UvVersion string `toml:"uv-version"`

	// BaseImage is the image reference to use for the container.
	BaseImage string `toml:"base-image"`
}

func New(
	// +optional
	sdkSourceDir *Directory,
) *PythonSdk {
	return &PythonSdk{
		Discovery: NewDiscovery(UserConfig{
			UseUv:     true,
			UvVersion: getRequirement("uv"),
		}),
		// TODO: get an sdist build of the SDK into the engine rather than
		// duplicating which files to include in the engine's publishing task.
		SDKSourceDir: sdkSourceDir.
			WithoutDirectory("runtime").
			// TODO: remove the codegen CLI from the SDK source
			// as it doesn't apply to Python.
			WithoutFile("codegen"),
		Container: dag.Container(),
	}
}

//go:embed template/main.py
var tplMain string

// Functions for building the runtime module for the Python SDK.
//
// The server interacts directly with the ModuleRuntime and Codegen functions.
// The others were built to be composable and chainable to facilitate the
// creation of extension modules (custom SDKs that depend on this one).
type PythonSdk struct {
	SDKSourceDir  *Directory
	RequiredPaths []string

	// Resulting container after each composing step.
	Container *Container

	// Discovery holds the logic for getting more information from the target module.
	// +private
	Discovery *Discovery
}

func (m *PythonSdk) Codegen(ctx context.Context, modSource *ModuleSource, introspectionJson string) (*GeneratedCode, error) {
	ctr, err := m.Common(ctx, modSource, introspectionJson)
	if err != nil {
		return nil, err
	}
	return dag.GeneratedCode(ctr.Directory(ModSourceDirPath)).
		WithVCSGeneratedPaths(
			[]string{GenDir + "/**"},
		).
		WithVCSIgnoredPaths(
			[]string{GenDir},
		), nil
}

func (m *PythonSdk) ModuleRuntime(
	ctx context.Context,
	modSource *ModuleSource,
	introspectionJson string,
) (*Container, error) {
	ctr, err := m.Common(ctx, modSource, introspectionJson)
	if err != nil {
		return nil, err
	}
	return ctr.WithEntrypoint([]string{RuntimeExecutablePath}), nil
}

// Common steps for the ModuleRuntime and Codegen functions.
func (m *PythonSdk) Common(ctx context.Context, modSource *ModuleSource, introspectionJson string) (*Container, error) {
	// The following functions were built to be composable in a granular way,
	// to allow a custom SDK to depend on this one and hook into before or
	// after major steps in the process. For example, you can get the base
	// container, add system packages, use the new one with `WithContainer`,
	// and then continue with the rest of the steps. Without this, you'd need
	// to copy the entire function and modify it.

	// In extension modules, Load is chainable.
	m, err := m.Load(ctx, modSource)
	if err != nil {
		return nil, err
	}
	ctr := m.
		WithBase().
		WithTemplate().
		WithSDK(GenDir, introspectionJson).
		WithSource().
		Container
	return ctr, nil
}

// Get all the needed information from the module's metadata and source files.
func (m *PythonSdk) Load(ctx context.Context, modSource *ModuleSource) (*PythonSdk, error) {
	if err := m.Discovery.Load(ctx, modSource); err != nil {
		return nil, fmt.Errorf("runtime module load: %v", err)
	}
	return m, nil
}

// Initialize the container with the base image and installer
//
// Workdir is set to the module's source directory.
func (m *PythonSdk) WithBase() *PythonSdk {
	base := dag.Container().
		From(m.BaseImage()).
		WithEnvVariable("PYTHONUNBUFFERED", "1").
		WithEnvVariable("PIP_DISABLE_PIP_VERSION_CHECK", "1").
		WithEnvVariable("PIP_ROOT_USER_ACTION", "ignore").
		WithMountedCache("/root/.cache/pip", m.cacheVolume("pip")).
		// for debugging
		WithDefaultTerminalCmd([]string{"/bin/bash"})

	if m.UseUv() {
		uv := base.
			WithEnvVariable("PYTHONDONTWRITEBYTECODE", "1").
			WithNewFile("reqs.txt", ContainerWithNewFileOpts{
				Contents: "uv" + m.Discovery.UserConfig().UvVersion,
			}).
			WithExec([]string{"pip", "install", "-r", "/reqs.txt"}).
			File("/usr/local/bin/uv")
		base = base.
			WithFile("/usr/local/bin/uv", uv).
			WithMountedCache("/root/.cache/uv", m.cacheVolume("uv")).
			// Use a clean venv with uv
			WithExec([]string{"uv", "venv", VenvPath}).
			WithEnvVariable("VIRTUAL_ENV", VenvPath).
			WithEnvVariable("PATH", "$VIRTUAL_ENV/bin:$PATH", ContainerWithEnvVariableOpts{
				Expand: true,
			})
	}

	m.Container = base.WithWorkdir(path.Join(ModSourceDirPath, m.Discovery.SubPath))

	return m
}

// Add the template files, to skafold a new module
//
// The following files are added:
// - /runtime
// - pyproject.toml
// - requirements.lock
// - src/main/__init__.py
func (m *PythonSdk) WithTemplate() *PythonSdk {
	template := dag.CurrentModule().Source().Directory("template")
	d := m.Discovery

	m.Container = m.Container.WithFile(
		RuntimeExecutablePath,
		template.File("runtime.py"),
		ContainerWithFileOpts{Permissions: 0o755},
	)

	if d.IsInit {
		d.AddFile("pyproject.toml", template.File("pyproject.toml"))

		if m.UseUv() && !d.HasFile(LockFilePath) {
			sdkToml := path.Join(GenDir, "pyproject.toml")
			d.AddLockFile(m.Container.
				WithMountedFile(sdkToml, m.SDKSourceDir.File("pyproject.toml")).
				WithExec([]string{"uv", "pip", "compile", "--generate-hashes", "-o", LockFilePath, sdkToml}).
				File(LockFilePath),
			)
		}

		if !d.HasFile("*.py") {
			d.AddNewFile(
				MainFilePath,
				strings.ReplaceAll(tplMain, MainObjectName, strcase.ToCamel(d.ModName)),
			)
		}
	}

	return m
}

// Add the SDK package to the source directory
//
// This also installs the package in the current virtual environemnt and
// regenerates the client for the current API schema.
func (m *PythonSdk) WithSDK(sdkPath string, introspectionJson string) *PythonSdk {
	// Add the clean copy of the SDK  to the source directory, to avoid
	// extrenuous files from build, *.pyc, or others.
	m.Discovery.AddDirectory(sdkPath, m.SDKSourceDir)

	// Don't want to mount the entire source directory yet, to avoid cache
	// invalidation. The full context directory will be mounted later, which
	// will replace the mounts declared here.
	ctr := m.Container.WithMountedDirectory(GenDir, m.SDKSourceDir)

	// Support installing directly from a requirements.lock file to allow
	// pinning dependencies.
	if m.Discovery.HasFile(LockFilePath) {
		ctr = ctr.
			WithFile(LockFilePath, m.Discovery.GetFile(LockFilePath)).
			// Don't install the current project yet.
			WithExec([]string{
				"sed", "-i",
				"-e", `/-e file:\./d`,
				"-e", `/-e \./d`,
				LockFilePath,
			}).
			With(m.install("-r", LockFilePath))
	}

	// Generate the client.
	// This is not strictly necessary on init (just call), but warms the cache.

	// Need to install SDK for the codegen script.
	ctr = ctr.With(m.install("-e", sdkPath))

	// Allow empty introspection to facilitate debugging the container with a
	// `dagger call module-runtime terminal --cmd=bash` command.
	if introspectionJson != "" {
		genPath := path.Join(sdkPath, GenPath)

		genFile := ctr.
			WithNewFile(SchemaPath, ContainerWithNewFileOpts{
				Contents: introspectionJson,
			}).
			WithExec([]string{
				"python", "-m", "dagger", "codegen",
				"--output", genPath,
				"--introspection", SchemaPath,
			}, ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true,
			}).
			File(genPath)

		m.Discovery.AddFile(genPath, genFile)

		// Mount the generated file for debugging purposes.
		// It'll get replaced later with the ContextDir mount.
		ctr = ctr.WithMountedFile(genPath, genFile)
	}

	m.Container = ctr

	return m
}

// Add the module's source files and install the package
func (m *PythonSdk) WithSource() *PythonSdk {
	m.Container = m.Container.
		WithMountedDirectory(ModSourceDirPath, m.Discovery.ContextDir).
		With(m.install("-e", "."))
	return m
}
