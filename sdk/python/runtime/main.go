// Runtime module for the Python SDK

package main

import (
	"context"
	_ "embed"
	"fmt"
	"path"
	"python-sdk/internal/dagger"
	"strings"

	"github.com/iancoleman/strcase"
)

const (
	ModSourceDirPath      = "/src"
	RuntimeExecutablePath = "/runtime"
	GenDir                = "sdk"
	GenPath               = "src/dagger/client/gen.py"
	SchemaPath            = "/schema.json"
	VenvPath              = "/opt/venv"
	PipCompileLock        = "requirements.lock"
	UvLock                = "uv.lock" // EXPERIMENTAL
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
	// Directory with the Python SDK source code.
	// +optional
	// +defaultPath="."
	// +ignore=["!src"]
	sdkSourceDir *dagger.Directory,
) *PythonSdk {
	if sdkSourceDir == nil {
		sdkSourceDir = dag.Directory()
	}
	return &PythonSdk{
		Discovery: NewDiscovery(UserConfig{
			UseUv: true,
		}),
		// TODO: get an sdist build of the SDK into the engine rather than
		// duplicating which files to include in the engine's publishing task.
		SdkSourceDir: sdkSourceDir.
			WithoutDirectory("runtime").
			WithoutDirectory(".venv").
			WithoutDirectory("codegen/.venv").
			WithoutDirectory("codegen/tests"),
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
	// Directory with the Python SDK source code
	SdkSourceDir *dagger.Directory

	// List of patterns to always include when loading Python modules
	RequiredPaths []string

	// Resulting container after each composing step
	Container *dagger.Container

	// Discovery holds the logic for getting more information from the target module
	// +private
	Discovery *Discovery
}

// Generated code for the Python module
func (m *PythonSdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.GeneratedCode, error) {
	self, err := m.Common(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}
	// TODO: inspect the result here
	return dag.GeneratedCode(self.Container.Directory(ModSourceDirPath)).
		WithVCSGeneratedPaths(
			[]string{GenDir + "/**"},
		).
		WithVCSIgnoredPaths(
			[]string{GenDir},
		), nil
}

// Container for executing the Python module runtime
func (m *PythonSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	self, err := m.Common(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}
	return self.
		WithInstall().
		Container.
		WithEntrypoint([]string{RuntimeExecutablePath}), nil
}

// Common steps for the ModuleRuntime and Codegen functions.
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
		WithTemplate().
		WithSDK(introspectionJSON).
		WithSource(), nil

}

// Get all the needed information from the module's metadata and source files
func (m *PythonSdk) Load(ctx context.Context, modSource *dagger.ModuleSource) (*PythonSdk, error) {
	if err := m.Discovery.Load(ctx, modSource); err != nil {
		return nil, fmt.Errorf("runtime module load: %w", err)
	}
	return m, nil
}

// Initialize the container with the base image and installer
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
	uvAddr, err := m.UvImage()
	if err != nil {
		return nil, err
	}

	m.Container = dag.Container().
		// Base Python
		From(baseAddr).
		WithEnvVariable("PYTHONUNBUFFERED", "1").
		WithEnvVariable("DAGGER_BASE_IMAGE", baseAddr).
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
		WithEnvVariable("DAGGER_UV_IMAGE", uvAddr).
		WithEnvVariable("UV_SYSTEM_PYTHON", "1").
		WithEnvVariable("UV_NATIVE_TLS", "1").
		WithWorkdir(path.Join(ModSourceDirPath, m.Discovery.SubPath))

	return m, nil
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
		dagger.ContainerWithFileOpts{Permissions: 0o755},
	)

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

	if d.IsInit {
		toml := "pyproject.toml"

		// On `dagger init --sdk`, one can first set a `pyproject.toml` to
		// change the base image, but if it's `dagger develop --sdk` the
		// existence of this file will exclude the rest from being added.
		if !d.HasFile(toml) {
			d.AddFile(toml, template.File(toml))
		}

		if m.UseUv() && !d.HasFile(UvLock) && !d.HasFile(PipCompileLock) {
			sdkToml := path.Join(GenDir, toml)
			d.AddLockFile(m.Container.
				WithMountedFile(sdkToml, m.SdkSourceDir.File(toml)).
				WithMountedFile(toml, d.GetFile(toml)).
				WithExec([]string{
					"uv", "pip", "compile", "-q",
					"-o", PipCompileLock,
					sdkToml,
					toml,
				}).
				File(PipCompileLock),
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
// This includes regenerating the client for the current API schema.
func (m *PythonSdk) WithSDK(introspectionJSON *dagger.File) *PythonSdk {
	// "codegen" dir included in the exported sdk directory to support
	// extending the runtime module in a custom SDK.
	m.Discovery.AddDirectory(GenDir, m.SdkSourceDir)

	// Allow empty introspection to facilitate debugging the container with a
	// `dagger call module-runtime terminal` command.
	if introspectionJSON != nil {
		genFile := m.Container.
			WithWorkdir("/codegen").
			WithMountedDirectory("", m.SdkSourceDir.Directory("codegen")).
			WithMountedFile(SchemaPath, introspectionJSON).
			WithExec([]string{
				"uv", "run", "--no-dev",
				"python", "-m",
				"codegen", "generate", "-i", SchemaPath, "-o", "/gen.py",
			}).
			File("/gen.py")

		m.Discovery.AddFile(path.Join(GenDir, GenPath), genFile)
	}

	return m
}

// Add the module's source files
func (m *PythonSdk) WithSource() *PythonSdk {
	toml := "pyproject.toml"
	sdkToml := path.Join(GenDir, toml)

	ctr := m.Container.WithMountedDirectory(ModSourceDirPath, m.Discovery.ContextDir)

	// Update lock file but without upgrading dependencies.
	if m.UseUv() && !m.Discovery.IsInit {
		switch {
		case m.Discovery.HasFile(UvLock):
			// Support uv.lock.
			ctr = ctr.WithExec([]string{"uv", "lock"})
		case m.Discovery.HasFile(PipCompileLock):
			// Support requirements.lock.
			ctr = ctr.WithExec([]string{
				"uv", "pip", "compile", "-q",
				"-o", PipCompileLock,
				sdkToml,
				toml,
			})
		}
	}

	m.Container = ctr

	return m
}

// Install the dependencies and the module
func (m *PythonSdk) WithInstall() *PythonSdk {
	// NB: We compile bytecode now to cache it in the image.

	ctr := m.Container

	// Support uv.lock for simple and fast project management workflow.
	if m.UseUv() && m.Discovery.HasFile(UvLock) {
		m.Container = ctr.
			WithExec([]string{"uv", "sync", "--no-dev", "--compile-bytecode"}).
			// Activate virtualenv for .venv to avoid having to prepend
			// `uv run` to the entrypoint.
			WithEnvVariable("VIRTUAL_ENV", path.Join(ModSourceDirPath, m.Discovery.SubPath, ".venv")).
			WithEnvVariable("PATH", "$VIRTUAL_ENV/bin:$PATH", dagger.ContainerWithEnvVariableOpts{
				Expand: true,
			})
		return m
	}

	// Fallback to pip-compile workflow.
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
		install = append([]string{"uv"}, append(install, "--compile-bytecode")...)
		check = append([]string{"uv"}, check...)
	}

	m.Container = ctr.
		WithExec(install).
		WithExec(check)

	return m
}
