// Runtime module for the Python SDK

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"path"
	"python-sdk/internal/dagger"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/introspection"
)

const (
	ModSourceDirPath      = "/src"
	RuntimeExecutablePath = "/runtime"
	GenDir                = "sdk"
	SDKGenPath            = "src/dagger/client/gen.py"
	UserGenPath           = "src/dagger_gen.py"
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
	// BaseImage is the image reference to use for the base container.
	BaseImage string `toml:"base-image"`

	// UseUv is for choosing the faster uv tool instead of pip to install packages.
	UseUv bool `toml:"use-uv"`

	// UvVersion is the version of the uv tool to use.
	//
	// By default, it's pinned to a specific version in each dagger version.
	UvVersion string `toml:"uv-version"`
}

func New(
	// Directory with the Python SDK source code.
	// +defaultPath=".."
	// +ignore=["**", "!pyproject.toml", "!uv.lock", "!src/**/*.py", "!src/**/*.typed", "!codegen/pyproject.toml", "!codegen/**/*.py", "!LICENSE", "!README.md", "!dist/*"]
	sdkSourceDir *dagger.Directory,
) (*PythonSdk, error) {
	// Shouldn't happen due to defaultPath, but just in case.
	if sdkSourceDir == nil {
		return nil, fmt.Errorf("sdk source directory not provided")
	}
	d, err := NewDiscovery(UserConfig{
		UseUv: true,
	})
	if err != nil {
		return nil, err
	}
	return &PythonSdk{
		Discovery:    d,
		SdkSourceDir: sdkSourceDir,
		Container:    dag.Container(),
		// TODO: remove the following when we no longer vendor every time
		VendorPath: GenDir,
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

	// Resulting container after each composing step
	Container *dagger.Container

	// The original module's name
	ModName string

	// The normalized python distribution package name (in pyproject.toml)
	ProjectName string

	// The normalized python import package name (in the filesystem)
	PackageName string

	// The normalized main object name in Python
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

	// Relative path to vendor client library into
	VendorPath string

	// True if the module is new and we need to create files from the template
	//
	// It's assumed that this is the case if there's no pyproject.toml file.
	IsInit bool

	// Discovery holds the logic for getting more information from the target module.
	// +private
	Discovery *Discovery

	// introspectionJSON holds the schema for dependency detection
	// +private
	introspectionJSON *dagger.File
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

	ignorePaths := []string{".venv", "**/__pycache__"}
	genPaths := []string{
		// TODO: uncomment when we start generating client bindings outside the library
		// UserGenPath,
	}

	if m.VendorPath != "" {
		ignorePaths = append(ignorePaths, m.VendorPath)
		genPaths = []string{m.VendorPath + "/**"}
	}

	return dag.GeneratedCode(m.Container.Directory(m.ContextDirPath)).
		WithVCSGeneratedPaths(genPaths).
		WithVCSIgnoredPaths(ignorePaths), nil
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
	// +optional
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

	// Store introspection JSON for template generation
	m.introspectionJSON = introspectionJSON

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
	baseAddr := m.getImage(BaseImageName).String()

	// NB: Adding env vars with container images that were pulled allows
	// modules to reuse them for performance benefits.
	m.Container = dag.Container().
		// Base Python
		From(baseAddr).
		// This var is informational only, in case it's useful in a module.
		WithEnvVariable("DAGGER_BASE_IMAGE", baseAddr).
		WithEnvVariable("PYTHONUNBUFFERED", "1").
		WithEnvVariable("PIP_DISABLE_PIP_VERSION_CHECK", "1").
		WithEnvVariable("PIP_ROOT_USER_ACTION", "ignore").
		// Uv
		With(m.uv()).
		WithEnvVariable("UV_SYSTEM_PYTHON", "1").
		WithEnvVariable("UV_LINK_MODE", "copy").
		WithEnvVariable("UV_NATIVE_TLS", "1").
		WithEnvVariable("UV_PROJECT_ENVIRONMENT", "/opt/venv")

	if !m.UseUv() {
		m.Container = m.Container.WithMountedCache("/root/.cache/pip", dag.CacheVolume("modpython-pip"))
	}
	if m.IndexURL() != "" {
		m.Container = m.Container.WithEnvVariable("UV_INDEX_URL", m.IndexURL())
	}
	if m.ExtraIndexURL() != "" {
		m.Container = m.Container.WithEnvVariable("UV_EXTRA_INDEX_URL", m.ExtraIndexURL())
	}

	return m, nil
}

func (m *PythonSdk) uv() dagger.WithContainerFunc {
	// NB: Always add uvImage to avoid a dynamic base pipeline as much as possible.
	// Even if users don't use it, it's useful to create a faster virtual env
	// and faster install for the codegen package.
	uvImage := m.getImage(UvImageName)

	bins := dag.Container().From(uvImage.String()).Rootfs()

	return func(ctr *dagger.Container) *dagger.Container {
		// Use bundled uv binaries if version wasn't overridden.
		if m.Discovery.SdkHasFile("dist/uv") && uvImage.Equal(m.Discovery.DefaultImages[UvImageName]) {
			bins = m.SdkSourceDir.Directory("dist")
		}

		return ctr.
			WithDirectory("/usr/local/bin", bins, dagger.ContainerWithDirectoryOpts{
				Include: []string{"uv*"}, // uv and uvx
			}).
			WithMountedCache("/root/.cache/uv", dag.CacheVolume("modpython-uv")).
			// These are informational only, to be leveraged by the target module if needed.
			WithEnvVariable("DAGGER_UV_IMAGE", uvImage.String()).
			WithEnvVariable("DAGGER_UV_VERSION", uvImage.Tag())
	}
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
			projCfg := strings.ReplaceAll(tplToml, "main", m.ProjectName)
			m.AddNewFile(ProjectCfg, VendorConfig(projCfg, m.VendorPath))
		}
		if !d.HasFile("*.py") {
			// Check if we have dependencies to generate passthrough functions
			mainSource, err := m.generateMainSource()
			if err != nil {
				// Fall back to default template if we can't parse dependencies
				mainSource = strings.ReplaceAll(tplMain, MainObjectName, m.MainObjectName)
			}

			m.AddNewFile(
				path.Join("src", m.PackageName, "__init__.py"),
				strings.ReplaceAll(tplInit, MainObjectName, m.MainObjectName),
			)
			m.AddNewFile(
				path.Join("src", m.PackageName, "main.py"),
				mainSource,
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
	if m.VendorPath != "" {
		src := m.SdkSourceDir
		// If not vendoring we don't care to remove this
		if m.Discovery.SdkHasFile("dist/") {
			src = src.WithoutDirectory("dist")
		}
		m.AddDirectory(m.VendorPath, src)
	}

	// Allow empty introspection to facilitate debugging the container with a
	// `dagger call module-runtime terminal` command.
	if introspectionJSON != nil {
		ctr := m.Container
		cmd := []string{"codegen"}

		// When not using the bundled codegen executable we can revert to executing directly
		if m.Discovery.SdkHasFile("dist/codegen") {
			ctr = ctr.
				WithMountedCache("/root/.shiv", dag.CacheVolume("shiv")).
				WithMountedFile("/usr/local/bin/codegen", m.SdkSourceDir.File("dist/codegen"))
		} else {
			ctr = ctr.
				WithWorkdir("/sdk").
				WithMountedDirectory("", m.SdkSourceDir)
			cmd = []string{
				"uv", "run", "--isolated", "--frozen", "--package", "codegen",
				"python", "-m", "codegen",
			}
		}

		genFile := ctr.
			// mounted schema as late as possible because it varies more often
			WithMountedFile(SchemaPath, introspectionJSON).
			WithExec(append(cmd, "generate", "-i", SchemaPath, "-o", "/gen.py")).
			File("/gen.py")

		genPath := UserGenPath

		// For now, patch vendored client library with generated bindings.
		// TODO: Always generate outside library, even if vendored.
		if m.VendorPath != "" {
			genPath = path.Join(m.VendorPath, SDKGenPath)
		}

		m.AddFile(genPath, genFile)
	}

	return m
}

// Add the module's source code
func (m *PythonSdk) WithSource() *PythonSdk {
	m.Container = m.Container.
		WithWorkdir(path.Join(m.ContextDirPath, m.SubPath)).
		WithMountedDirectory(m.ContextDirPath, m.ContextDir).
		// These are added as late as possible  to avoid cache invalidation
		// between different modules. It may be used by the runtime entrypoint
		// so only needed in ModuleRuntime but added here so that extension
		// modules get it for free since they need to reimplement ModuleRuntime.
		// It's ok since the previous layer is already dependent on the target
		// module's sources.
		WithEnvVariable("DAGGER_MODULE", m.ModName).
		WithEnvVariable("DAGGER_DEFAULT_PYTHON_PACKAGE", m.PackageName).
		WithEnvVariable("DAGGER_MAIN_OBJECT", m.MainObjectName)
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
		args := []string{
			"uv", "pip", "compile", "-q", "--universal",
			"-o", PipCompileLock,
			ProjectCfg,
		}

		if m.VendorPath != "" {
			args = append(args, path.Join(m.VendorPath, ProjectCfg))
		}

		ctr = ctr.WithExec(args)
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

// generateMainSource generates the main.py source code for a module.
// If dependencies are detected in the introspection JSON, it generates passthrough functions.
// Otherwise, it generates the default template.
func (m *PythonSdk) generateMainSource() (string, error) {
	// If no introspection JSON is available, use default template
	if m.introspectionJSON == nil {
		return strings.ReplaceAll(tplMain, MainObjectName, m.MainObjectName), nil
	}

	ctx := context.Background()

	// Read the introspection JSON
	introspectionContent, err := m.introspectionJSON.Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("read introspection JSON: %w", err)
	}

	// Parse the introspection response (which wraps the schema in __schema field)
	var response introspection.Response
	if err := json.Unmarshal([]byte(introspectionContent), &response); err != nil {
		return "", fmt.Errorf("parse introspection response: %w", err)
	}

	// Get the schema from the response
	schema := response.Schema
	if schema == nil {
		return "", fmt.Errorf("schema is nil in introspection response")
	}

	// Extract dependencies
	dependencies := schema.ExtractDependencies()
	if len(dependencies) == 0 {
		// No dependencies, use default template
		return strings.ReplaceAll(tplMain, MainObjectName, m.MainObjectName), nil
	}

	// Generate passthrough module
	return generatePythonPassthrough(m.MainObjectName, dependencies), nil
}

// generatePythonPassthrough generates Python code with passthrough functions for dependencies
func generatePythonPassthrough(moduleName string, dependencies []*introspection.DependencyModule) string {
	var sb strings.Builder

	// Module docstring
	sb.WriteString(fmt.Sprintf(`"""A generated module for %s functions

This module has been generated from a blueprint and provides passthrough
functions to the original module.
"""

import dagger
from dagger import dag, function, object_type


@object_type
class %s:
`, moduleName, moduleName))

	// Generate passthrough functions for each dependency
	for _, dep := range dependencies {
		depAccessor := toSnakeCase(dep.Name)

		for _, field := range dep.Functions {
			// Skip deprecated functions
			if field.IsDeprecated {
				continue
			}

			// Skip internal Id function
			if strings.ToLower(field.Name) == "id" {
				continue
			}

			funcSnake := toSnakeCase(field.Name)

			// Build parameter list (only required args)
			var params []string
			var argsList []string
			params = append(params, "self")

			for _, arg := range field.Args {
				// Skip optional arguments
				if arg.TypeRef.IsOptional() {
					continue
				}

				argSnake := toSnakeCase(arg.Name)
				argType := formatPythonType(arg.TypeRef)
				params = append(params, fmt.Sprintf("%s: %s", argSnake, argType))
				argsList = append(argsList, argSnake)
			}

			// Determine return type and if async is needed
			returnType := formatPythonType(field.TypeRef)
			isAsync := !isObjectType(field.TypeRef)

			// Generate function
			sb.WriteString("\n    @function\n")

			if isAsync {
				sb.WriteString(fmt.Sprintf("    async def %s(%s) -> %s:\n", funcSnake, strings.Join(params, ", "), returnType))
			} else {
				sb.WriteString(fmt.Sprintf("    def %s(%s) -> %s:\n", funcSnake, strings.Join(params, ", "), returnType))
			}

			// Add docstring
			if field.Description != "" {
				lines := strings.Split(strings.TrimSpace(field.Description), "\n")
				sb.WriteString(fmt.Sprintf("        \"\"\"%s\n", lines[0]))
				for _, line := range lines[1:] {
					sb.WriteString(fmt.Sprintf("        %s\n", line))
				}
				sb.WriteString("        \"\"\"\n")
			} else {
				sb.WriteString(fmt.Sprintf("        \"\"\"Calls the %s function from the %s dependency\"\"\"\n", field.Name, dep.Name))
			}

			// Build the function call
			callChain := fmt.Sprintf("dag.%s().%s(%s)", depAccessor, funcSnake, strings.Join(argsList, ", "))

			if isAsync {
				sb.WriteString(fmt.Sprintf("        return await %s\n", callChain))
			} else {
				sb.WriteString(fmt.Sprintf("        return %s\n", callChain))
			}
		}
	}

	return sb.String()
}

// toSnakeCase converts a string to snake_case
func toSnakeCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteRune('_')
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}

// formatPythonType converts an introspection TypeRef to a Python type annotation
func formatPythonType(typeRef *introspection.TypeRef) string {
	if typeRef == nil {
		return "Any"
	}

	switch typeRef.Kind {
	case introspection.TypeKindNonNull:
		// For non-null, just recurse
		return formatPythonType(typeRef.OfType)
	case introspection.TypeKindList:
		return "list[" + formatPythonType(typeRef.OfType) + "]"
	case introspection.TypeKindScalar:
		return mapPythonScalarType(typeRef.Name)
	case introspection.TypeKindEnum:
		return typeRef.Name
	case introspection.TypeKindObject:
		return "dagger." + typeRef.Name
	default:
		return "Any"
	}
}

// mapPythonScalarType maps GraphQL scalar types to Python types
func mapPythonScalarType(typeName string) string {
	switch typeName {
	case "ID":
		return "str"
	case "String":
		return "str"
	case "Int":
		return "int"
	case "Float":
		return "float"
	case "Boolean":
		return "bool"
	default:
		return typeName
	}
}

// isObjectType checks if a TypeRef represents an object type
func isObjectType(typeRef *introspection.TypeRef) bool {
	if typeRef == nil {
		return false
	}

	// Unwrap non-null and list
	actualType := typeRef
	for actualType.Kind == introspection.TypeKindNonNull || actualType.Kind == introspection.TypeKindList {
		if actualType.OfType == nil {
			break
		}
		actualType = actualType.OfType
	}

	return actualType.Kind == introspection.TypeKindObject
}
