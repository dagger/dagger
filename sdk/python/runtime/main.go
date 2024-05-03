// Runtime module for the Python SDK

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
	// Directory with the Python SDK source code.
	// +optional
	sdkSourceDir *Directory,
) *PythonSdk {
	if sdkSourceDir == nil {
		sdkSourceDir = dag.Directory()
	}
	return &PythonSdk{
		Discovery: NewDiscovery(UserConfig{
			UseUv:     true,
			UvVersion: getRequirement("uv"),
		}),
		// TODO: get an sdist build of the SDK into the engine rather than
		// duplicating which files to include in the engine's publishing task.
		SDKSourceDir: sdkSourceDir.WithoutDirectory("runtime"),
		Container:    dag.Container(),
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
	SDKSourceDir *Directory

	// List of patterns to allways include when loading Python modules
	RequiredPaths []string

	// Resulting container after each composing step
	Container *Container

	// Discovery holds the logic for getting more information from the target module
	// +private
	Discovery *Discovery
}

// Generated code for the Python module
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

// Container for executing the Python module runtime
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
		WithSDK(introspectionJson).
		WithSource().
		Container
	return ctr, nil
}

// Get all the needed information from the module's metadata and source files
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
		WithEnvVariable("DAGGER_BASE_IMAGE", m.BaseImage()).
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

		if m.UseUv() && !d.HasFile(LockFilePath) {
			sdkToml := path.Join(GenDir, toml)
			d.AddLockFile(m.Container.
				WithMountedFile(sdkToml, m.SDKSourceDir.File(toml)).
				WithMountedFile(toml, d.GetFile(toml)).
				WithExec([]string{
					"uv", "pip", "compile", "-q",
					"--generate-hashes",
					"-o", LockFilePath,
					sdkToml,
					toml,
				}).
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
// This includes regenerating the client for the current API schema.
func (m *PythonSdk) WithSDK(introspectionJson string) *PythonSdk {
	// "codegen" dir included in the exported sdk directory to support
	// extending the runtime module in a custom SDK.
	m.Discovery.AddDirectory(GenDir, m.SDKSourceDir)

	// Allow empty introspection to facilitate debugging the container with a
	// `dagger call module-runtime terminal` command.
	if introspectionJson != "" {
		genFile := m.Container.
			WithMountedDirectory("/codegen", m.SDKSourceDir.Directory("codegen")).
			WithWorkdir("/codegen").
			With(m.install("-r", LockFilePath)).
			WithNewFile(SchemaPath, ContainerWithNewFileOpts{
				Contents: introspectionJson,
			}).
			WithExec([]string{
				"python", "-m", "codegen", "generate", "-i", SchemaPath, "-o", "/gen.py",
			}).
			File("/gen.py")

		m.Discovery.AddFile(path.Join(GenDir, GenPath), genFile)
	}

	return m
}

// Add the module's source files and install
func (m *PythonSdk) WithSource() *PythonSdk {
	toml := "pyproject.toml"
	sdkToml := path.Join(GenDir, toml)

	ctr := m.Container.WithMountedDirectory(ModSourceDirPath, m.Discovery.ContextDir)

	// Support installing directly from a requirements.lock file to allow
	// pinning dependencies.
	if m.Discovery.HasFile(LockFilePath) {
		if m.UseUv() && !m.Discovery.IsInit {
			ctr = ctr.WithExec([]string{
				"uv", "pip", "compile", "-q",
				"--generate-hashes",
				"-o", LockFilePath,
				sdkToml,
				toml,
			})
		}
		// Install from lock separately to editable installs because of hashes.
		ctr = ctr.With(m.install("-r", LockFilePath))
	}

	// Install the SDK as editable because of the generated client
	ctr = ctr.With(m.install("-e", "./sdk", "-e", "."))

	cmd := []string{"pip", "check"}
	if m.UseUv() {
		cmd = append([]string{"uv"}, cmd...)
	}
	m.Container = ctr.WithExec(cmd)

	return m
}
