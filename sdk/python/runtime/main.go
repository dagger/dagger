// Runtime module for the Python SDK

package main

import (
	"context"
	_ "embed"
	"fmt"
	"path"
	"python-sdk/internal/dagger"
	"strings"
)

const (
	ModSourceDirPath      = "/src"
	RuntimeExecutablePath = "/runtime"
	GenDir                = "sdk"
	GenPath               = "src/dagger/client/gen.py"
	SchemaPath            = "/schema.json"
	VenvPath              = "/opt/venv"
	ProjectCfg            = "pyproject.toml"
	PipCompileLock        = "requirements.lock"
	UvLock                = "uv.lock"
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
	// By default, it's pinned to a specific version in each dagger version.
	// Can be useful to get a newer version to fix a bug or get a new feature.
	UvVersion string `toml:"uv-version"`

	// BaseImage is the image reference to use for the base container.
	BaseImage string `toml:"base-image"`
}

func New(
	// Directory with the Python SDK source code.
	// +defaultPath=".."
	// +ignore=["**", "!pyproject.toml", "!uv.lock", "!src/**/*.py", "!src/**/*.typed", "!codegen/pyproject.toml", "!codegen/**/*.py", "!LICENSE", "!README.md"]
	sdkSourceDir *dagger.Directory,
) (*PythonSdk, error) {
	// Shouldn't happen due to defaultPath, but just in case.
	if sdkSourceDir == nil {
		return nil, fmt.Errorf("sdk source directory not provided")
	}
	return &PythonSdk{
		Discovery: NewDiscovery(UserConfig{
			UseUv: true,
		}),
		SdkSourceDir: sdkSourceDir.WithoutDirectory("runtime"),
		Container:    dag.Container(),
	}, nil
}

//go:embed template/pyproject.toml
var tplToml string

//go:embed template/__init__.py
var tplInit string

//go:embed template/main.py
var tplMain string

// Functions for building the runtime module for the Python SDK.
//
// The server interacts directly with the ModuleRuntime and Codegen functions.
// The others were built to be composable and chainable to facilitate the
// creation of extension modules (custom SDKs that depend on this one).
type PythonSdk struct {
	// Directory with the Python SDK source code
	SdkSourceDir *dagger.Directory

	// List of patterns to always include when loading Python modules
	RequiredPaths []string

	// Resulting container after each composing step
	Container *dagger.Container

	// The original module's name
	ModName string

	// The normalized python distribution package name (in pyproject.toml)
	ProjectName string

	// The normalized python import package name (in the filesystem)
	PackageName string

	// The normalized main object name in python
	MainObjectName string

	// The source needed to load and run a module
	ModSource *dagger.ModuleSource

	// ContextDir is a copy of the context directory from the module source
	//
	// We add files to this directory, always joining paths with the source's
	// subpath. We could use modSource.Directory("") for that if it was read-only,
	// but since we have to mount the context directory in the end, rather than
	// mounting the context dir and then mounting the forked source dir on top,
	// we fork the context dir instead so there's only one mount in the end.
	ContextDir *dagger.Directory

	// ContextDirPath is a unique host path for the module being loaded
	//
	// HACK: this property is computed as a unique value for a ModuleSource to
	// provide a unique path on the filesystem. This is because the uv cache
	// uses hashes of source paths - so we need to have something unique, or we
	// can get very real conflicts in the uv cache.
	ContextDirPath string

	// Relative path from the context directory to the source directory
	SubPath string

	// True if the module is new and we need to create files from the template
	//
	// It's assumed that this is the case if there's no pyproject.toml file.
	IsInit bool

	// Discovery holds the logic for getting more information from the target module.
	// +private
	Discovery *Discovery
}

// Generated code for the Python module
func (m *PythonSdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.GeneratedCode, error) {
	m, err := m.Common(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}
	return dag.GeneratedCode(m.Container.Directory(m.ContextDirPath)).
		WithVCSGeneratedPaths(
			[]string{GenDir + "/**"},
		).
		WithVCSIgnoredPaths(
			[]string{GenDir, ".venv", "**/__pycache__"},
		), nil
}

// Container for executing the Python module runtime
func (m *PythonSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	m, err := m.Common(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}
	return m.WithInstall().Container, nil
}

// Common steps for the ModuleRuntime and Codegen functions
func (m *PythonSdk) Common(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*PythonSdk, error) {
	// The following functions were built to be composable in a granular way,
	// to allow a custom SDK to depend on this one and hook into before or
	// after major steps in the process. For example, you can get the base
	// container, add system packages, use the new one with `WithContainer`,
	// and then continue with the rest of the steps. Without this, you'd need
	// to copy the entire function and modify it.

	// NB: In extension modules, Load is chainable.
	_, err := m.Load(ctx, modSource)
	if err != nil {
		return nil, err
	}
	_, err = m.WithBase()
	if err != nil {
		return nil, err
	}
	return m.
		WithSDK(introspectionJSON).
		WithTemplate().
		WithSource().
		WithUpdates(), nil
}

// Get all the needed information from the module's metadata and source files
func (m *PythonSdk) Load(ctx context.Context, modSource *dagger.ModuleSource) (*PythonSdk, error) {
	m.ModSource = modSource
	m.ContextDir = modSource.ContextDirectory()

	if err := m.Discovery.Load(ctx, m); err != nil {
		return nil, fmt.Errorf("runtime module load: %w", err)
	}

	return m, nil
}

// Initialize the base Python container
//
// Workdir is set to the module's source directory.
func (m *PythonSdk) WithBase() (*PythonSdk, error) {
	baseImage, err := m.Discovery.GetImage(BaseImageName)
	if err != nil {
		return nil, err
	}
	baseAddr := baseImage.String()
	baseTag := baseImage.Tag()

	// NB: Always add uvImage to avoid a dynamic base pipeline as much as possible.
	// Even if users don't use it, it's useful to create a faster virtual env
	// and faster install for the codegen package.
	uvImage, err := m.Discovery.GetImage(UvImageName)
	if err != nil {
		return nil, err
	}
	uvAddr := uvImage.String()
	uvTag := uvImage.Tag()

	// NB: Adding env vars with container images that were pulled allows
	// modules to reuse them for performance benefits.
	m.Container = dag.Container().
		// Base Python
		From(baseAddr).
		WithEnvVariable("PYTHONUNBUFFERED", "1").
		// Pip
		WithEnvVariable("PIP_DISABLE_PIP_VERSION_CHECK", "1").
		WithEnvVariable("PIP_ROOT_USER_ACTION", "ignore").
		WithMountedCache("/root/.cache/pip", dag.CacheVolume("modpython-pip-"+baseTag)).
		// Uv
		WithDirectory(
			"/usr/local/bin",
			dag.Container().From(uvAddr).Rootfs(),
			dagger.ContainerWithDirectoryOpts{
				Include: []string{"uv*"},
			},
		).
		WithMountedCache("/root/.cache/uv", dag.CacheVolume("modpython-uv")).
		WithEnvVariable("UV_SYSTEM_PYTHON", "1").
		WithEnvVariable("UV_LINK_MODE", "copy").
		WithEnvVariable("UV_NATIVE_TLS", "1").
		WithEnvVariable("UV_PROJECT_ENVIRONMENT", "/opt/venv").
		WithWorkdir(path.Join(m.ContextDirPath, m.SubPath)).
		WithEnvVariable("DAGGER_MODULE", m.ModName).
		WithEnvVariable("DAGGER_MAIN_OBJECT", m.MainObjectName).
		WithEnvVariable("DAGGER_PYTHON_PACKAGE", m.PackageName).
		// These are informational only, to be leveraged by the target module
		// if needed.
		WithEnvVariable("DAGGER_BASE_IMAGE", baseAddr).
		WithEnvVariable("DAGGER_UV_IMAGE", uvAddr).
		WithEnvVariable("DAGGER_UV_VERSION", uvTag)

	if m.IndexURL() != "" {
		m.Container = m.Container.WithEnvVariable("UV_INDEX_URL", m.IndexURL())
	}
	if m.ExtraIndexURL() != "" {
		m.Container = m.Container.WithEnvVariable("UV_EXTRA_INDEX_URL", m.ExtraIndexURL())
	}

	return m, nil
}

// Add the template files to skaffold a new module
//
// The following files are added:
// - /runtime
// - <source>/pyproject.toml
// - <source>/src/<package_name>/__init__.py
// - <source>/src/<package_name>/main.py
func (m *PythonSdk) WithTemplate() *PythonSdk {
	m.Container = m.Container.
		WithFile(
			RuntimeExecutablePath,
			dag.CurrentModule().Source().File("template/runtime.py"),
			dagger.ContainerWithFileOpts{Permissions: 0o755},
		).
		WithEntrypoint([]string{RuntimeExecutablePath})

	d := m.Discovery

	// NB: We can't detect if it's a new module with `dagger develop --sdk`
	// if there's also a pyproject.toml file to customize the base container.
	//
	// The reason for adding sources only on new modules is because it's
	// been reported that it's surprising for users to delete the pyhton
	// file on the host and not fail on `dagger functions` and `dagger call`,
	// if we always recreate from the template. That's because only `init`
	// and `develop` export the generated files back to the host, potentially
	// creating a discrepancy.
	//
	// Throwing an error on missing files when not a new module is less
	// surprising, which is done during discovery.

	if m.IsInit {
		// On `dagger init --sdk`, one can first set a `pyproject.toml` to
		// change the base image, but if it's `dagger develop --sdk` the
		// existence of this file will set d.IsInit = true, thus skipping
		// this entire branch.
		if !d.HasFile(ProjectCfg) {
			m.AddNewFile(
				ProjectCfg,
				strings.ReplaceAll(tplToml, "main", m.ProjectName),
			)
		}
		if !d.HasFile("*.py") {
			m.AddNewFile(
				path.Join("src", m.PackageName, "__init__.py"),
				strings.ReplaceAll(tplInit, MainObjectName, m.MainObjectName),
			)
			m.AddNewFile(
				path.Join("src", m.PackageName, "main.py"),
				strings.ReplaceAll(tplMain, MainObjectName, m.MainObjectName),
			)
		}
	}

	return m
}

// Add the SDK package to the source directory
//
// This includes regenerating the client bindings for the current API schema
// (codegen).
func (m *PythonSdk) WithSDK(introspectionJSON *dagger.File) *PythonSdk {
	m.AddDirectory(GenDir, m.SdkSourceDir)

	// Allow empty introspection to facilitate debugging the container with a
	// `dagger call module-runtime terminal` command.
	if introspectionJSON != nil {
		genFile := m.Container.
			WithMountedDirectory("", m.SdkSourceDir).
			WithMountedFile(SchemaPath, introspectionJSON).
			WithExec([]string{
				"uv", "run", "--isolated", "--frozen", "--package", "codegen",
				"python", "-m", "codegen", "generate", "-i", SchemaPath, "-o", "/gen.py",
			}).
			File("/gen.py")

		m.AddFile(path.Join(GenDir, GenPath), genFile)
	}

	return m
}

// Add the module's source code
func (m *PythonSdk) WithSource() *PythonSdk {
	m.Container = m.Container.WithMountedDirectory(m.ContextDirPath, m.ContextDir)
	return m
}

// Make any updates to current source
func (m *PythonSdk) WithUpdates() *PythonSdk {
	if !m.UseUv() {
		return m
	}

	ctr := m.Container
	d := m.Discovery

	// Update lock file but without upgrading dependencies.
	switch {
	case m.UseUvLock():
		// Support uv.lock. Takes precedence.
		// Always update if uv.lock exists, but only create a new uv.lock
		// if init and there's not already a requirements.lock.
		ctr = ctr.WithExec([]string{"uv", "lock"})

	case d.HasFile(PipCompileLock) && !m.IsInit:
		// Support requirements.lock (legacy).
		ctr = ctr.WithExec([]string{
			"uv", "pip", "compile", "-q", "--universal",
			"-o", PipCompileLock,
			path.Join(GenDir, ProjectCfg),
			ProjectCfg,
		})
	}

	m.Container = ctr

	return m
}

// Install the module's package and dependencies
func (m *PythonSdk) WithInstall() *PythonSdk {
	// NB: Only enable bytecode compilation in `dagger call`
	// (not `dagger init/develop`), to avoid having to remove the .pyc files
	// before exporting the module back to the host.
	ctr := m.Container.WithEnvVariable("UV_COMPILE_BYTECODE", "1")

	// Support uv.lock for simple and fast project management workflow.
	if m.UseUvLock() {
		// While best practice is to sync dependencies first with only pyproject.toml and
		// uv.lock, user projects can have more required files for a minimally successful
		// `uv sync --no-install-project --no-dev`.
		// Besides, uv is fast enough that's not too bad to skip this optimization.
		m.Container = ctr.
			WithExec([]string{"uv", "sync", "--no-dev"}).
			// Activate virtualenv to avoid having to prepend `uv run` to the entrypoint.
			WithEnvVariable("VIRTUAL_ENV", "$UV_PROJECT_ENVIRONMENT", dagger.ContainerWithEnvVariableOpts{
				Expand: true,
			}).
			WithEnvVariable("PATH", "$VIRTUAL_ENV/bin:$PATH", dagger.ContainerWithEnvVariableOpts{
				Expand: true,
			})
		return m
	}

	// Fallback to pip-compile workflow (legacy).
	install := []string{"pip", "install", "-e", "./sdk", "-e", "."}
	check := []string{"pip", "check"}

	// uv has a compatible API with pip
	if m.UseUv() {
		// Support requirements.lock.
		if m.Discovery.HasFile(PipCompileLock) {
			// If there's a lock file, we assume that all the dependencies are
			// included in it so we can avoid resolving for them to get a faster
			// install.
			install = append(install, "--no-deps", "-r", PipCompileLock)
		}
		// pip compiles by default, but not uv
		install = append([]string{"uv"}, install...)
		check = append([]string{"uv"}, check...)
	}

	m.Container = ctr.
		WithExec(install).
		WithExec(check)

	return m
}
