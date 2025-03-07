package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsdistconsts"

	"github.com/iancoleman/strcase"
	"golang.org/x/mod/semver"
)

type SupportedTSRuntime string

const (
	Bun  SupportedTSRuntime = "bun"
	Node SupportedTSRuntime = "node"
)

type SupportedPackageManager string

const (
	Yarn       SupportedPackageManager = "yarn"
	Pnpm       SupportedPackageManager = "pnpm"
	Npm        SupportedPackageManager = "npm"
	BunManager SupportedPackageManager = "bun"
)

const (
	PnpmDefaultVersion = "8.15.4"
	YarnDefaultVersion = "1.22.22"
	NpmDefaultVersion  = "10.7.0"
)

func New(
	// +optional
	sdkSourceDir *dagger.Directory,
) *TypescriptSdk {
	return &TypescriptSdk{
		SDKSourceDir: sdkSourceDir,
		moduleConfig: &moduleConfig{},
	}
}

type packageJSONConfig struct {
	PackageManager string `json:"packageManager"`
	Dagger         *struct {
		BaseImage string `json:"baseImage"`
		Runtime   string `json:"runtime"`
	} `json:"dagger"`
}

type moduleConfig struct {
	runtime        SupportedTSRuntime
	runtimeVersion string

	// Custom base image
	image             string
	packageJSONConfig *packageJSONConfig

	packageManager        SupportedPackageManager
	packageManagerVersion string

	source  *dagger.Directory
	entries map[string]bool

	name    string
	subPath string
}

type TypescriptSdk struct {
	SDKSourceDir *dagger.Directory

	moduleConfig *moduleConfig
}

const (
	ModSourceDirPath         = "/src"
	EntrypointExecutableFile = "__dagger.entrypoint.ts"

	SrcDir         = "src"
	GenDir         = "sdk"
	NodeModulesDir = "node_modules"

	schemaPath     = "/schema.json"
	codegenBinPath = "/codegen"
)

// ModuleRuntime returns a container with the node or bun entrypoint ready to be called.
func (t *TypescriptSdk) ModuleRuntime(ctx context.Context, modSource *dagger.ModuleSource, introspectionJSON *dagger.File) (*dagger.Container, error) {
	err := t.analyzeModuleConfig(ctx, modSource)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze module config: %w", err)
	}

	ctr, err := t.CodegenBase(ctx, modSource, introspectionJSON, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create codegen base: %w", err)
	}

	// Mount the entrypoint file
	ctr = ctr.WithMountedFile(
		t.moduleConfig.entrypointPath(),
		ctr.Directory("/opt/module/bin").File(EntrypointExecutableFile),
	)

	switch t.moduleConfig.runtime {
	case Bun:
		return ctr.
			WithEntrypoint([]string{"bun", t.moduleConfig.entrypointPath()}), nil
	case Node:
		return ctr.
			// need to specify --tsconfig because final runtime container will change working directory to a separate scratch
			// dir, without this the paths mapped in the tsconfig.json will not be used and js module loading will fail
			// need to specify --no-deprecation because the default package.json has no main field which triggers a warning
			// not useful to display to the user.
			WithEntrypoint([]string{"tsx", "--no-deprecation", "--tsconfig", t.moduleConfig.tsConfigPath(), t.moduleConfig.entrypointPath()}), nil
	default:
		return nil, fmt.Errorf("unknown runtime: %s", t.moduleConfig.runtime)
	}
}

// Codegen returns the generated API client based on user's module
func (t *TypescriptSdk) Codegen(ctx context.Context, modSource *dagger.ModuleSource, introspectionJSON *dagger.File) (*dagger.GeneratedCode, error) {
	err := t.analyzeModuleConfig(ctx, modSource)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze module config: %w", err)
	}

	// Get base container without dependencies installed.
	ctr, err := t.CodegenBase(ctx, modSource, introspectionJSON, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create codegen base: %w", err)
	}

	// Extract codegen directory
	codegen := dag.
		Directory().
		WithDirectory(
			"/",
			ctr.Directory(ModSourceDirPath),
			dagger.DirectoryWithDirectoryOpts{Exclude: []string{"**/node_modules", "**/.pnpm-store"}},
		)

	return dag.GeneratedCode(
		codegen,
	).
		WithVCSGeneratedPaths([]string{
			GenDir + "/**",
		}).
		WithVCSIgnoredPaths([]string{
			GenDir,
			"**/node_modules/**",
			"**/.pnpm-store/**",
		}), nil
}

func (t *TypescriptSdk) RequiredClientGenerationFiles() []string {
	return []string{
		"./package.json",
		"./tsconfig.json",
	}
}

func (t *TypescriptSdk) GenerateClient(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
	outputDir string,
	dev bool,
) (*dagger.Directory, error) {
	workdirPath := "/module"

	currentModuleDirectoryPath, err := modSource.SourceRootSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module source root subpath: %w", err)
	}

	ctr := dag.Container().
		From(tsdistconsts.DefaultNodeImageRef).
		WithoutEntrypoint().
		// Add client config update file
		WithMountedFile(
			"/opt/__tsclientconfig.updator.ts",
			dag.CurrentModule().Source().Directory("bin").File("__tsclientconfig.updator.ts"),
		).
		// install tsx from its bundled location in the engine image
		WithMountedDirectory("/usr/local/lib/node_modules/tsx", t.SDKSourceDir.Directory("/tsx_module")).
		WithExec([]string{"ln", "-s", "/usr/local/lib/node_modules/tsx/dist/cli.mjs", "/usr/local/bin/tsx"}).
		// Add dagger codegen binary.
		WithMountedFile(codegenBinPath, t.SDKSourceDir.File("/codegen")).
		// Mount the introspection file.
		WithMountedFile(schemaPath, introspectionJSON).
		// Mount the current module directory.
		WithDirectory(workdirPath, modSource.ContextDirectory()).
		WithWorkdir(filepath.Join(workdirPath, currentModuleDirectoryPath))

	codegenArgs := []string{
		"/codegen",
		"--lang", "typescript",
		"--output", outputDir,
		"--introspection-json-path", schemaPath,
		fmt.Sprintf("--dev=%t", dev),
		"--client-only",
	}

	dependencies, err := modSource.Dependencies(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module dependencies: %w", err)
	}

	// Add remote dependency reference to the codegen arguments.
	for _, dep := range dependencies {
		depKind, err := dep.Kind(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get dependency kind: %w", err)
		}

		if depKind != dagger.ModuleSourceKindGitSource {
			continue
		}

		depRef, err := dep.AsString(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get module dependency ref: %w", err)
		}

		codegenArgs = append(codegenArgs,
			"--dependencies-ref",
			depRef,
		)
	}

	ctr = ctr.
		// Execute the code generator using the given introspection file.
		WithExec(codegenArgs, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})

	if dev {
		ctr = ctr.WithDirectory("./sdk", t.SDKSourceDir.
			WithoutDirectory("codegen").
			WithoutDirectory("runtime").
			WithoutDirectory("tsx_module"),
		).
			WithExec([]string{"npm", "pkg", "set", "dependencies[@dagger.io/dagger]=./sdk"}).
			WithExec([]string{"tsx", "/opt/__tsclientconfig.updator.ts", "--dev=true", fmt.Sprintf("--library-dir=%s", outputDir)})
	} else {
		ctr = ctr.
			WithExec([]string{"npm", "pkg", "set", "dependencies[@dagger.io/dagger]=@dagger.io/dagger"}).
			WithExec([]string{"tsx", "/opt/__tsclientconfig.updator.ts", "--dev=false", fmt.Sprintf("--library-dir=%s", outputDir)})
	}

	return dag.Directory().WithDirectory("/", ctr.Directory(workdirPath)), nil
}

// CodegenBase returns a Container containing the SDK from the engine container
// and the user's code with a generated API based on what he did.
func (t *TypescriptSdk) CodegenBase(ctx context.Context, modSource *dagger.ModuleSource, introspectionJSON *dagger.File, installDep bool) (*dagger.Container, error) {
	base, err := t.Base()
	if err != nil {
		return nil, fmt.Errorf("failed to create codegen base: %w", err)
	}

	// Get a directory with the SDK sources installed and the generated client.
	sdk := t.
		addSDK().
		WithDirectory(".", t.generateClient(base, introspectionJSON))

	base = base.
		// Add template directory
		WithMountedDirectory("/opt/module", dag.CurrentModule().Source().Directory(".")).
		// Mount user's module configuration (without sources) and the generated client in it.
		WithDirectory(ModSourceDirPath,
			dag.Directory().WithDirectory("/", modSource.ContextDirectory(), dagger.DirectoryWithDirectoryOpts{
				Include: t.moduleConfigFiles(t.moduleConfig.subPath),
			})).
		WithDirectory(filepath.Join(t.moduleConfig.modulePath(), GenDir), sdk).
		WithWorkdir(t.moduleConfig.modulePath())

	base = t.configureModule(base)

	// Generate the appropriate lock file
	base, err = t.generateLockFile(base)
	if err != nil {
		return nil, fmt.Errorf("failed to generate lock file: %w", err)
	}

	// Install dependencies if needed.
	if installDep {
		base, err = t.installDependencies(base)
		if err != nil {
			return nil, fmt.Errorf("failed to install dependencies: %w", err)
		}
	}

	// Add user's source files
	base = base.WithDirectory(ModSourceDirPath,
		dag.Directory().WithDirectory("/", modSource.ContextDirectory(), dagger.DirectoryWithDirectoryOpts{
			// Include the rest of the user's module except config files to not override previous steps & SDKs.
			Exclude: append(t.moduleConfigFiles(t.moduleConfig.subPath), filepath.Join(t.moduleConfig.subPath, "sdk")),
		}),
	)

	base, err = t.addTemplate(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("failed to add template: %w", err)
	}

	return base, nil
}

// Returns a list of files to include for module configs.
func (t *TypescriptSdk) moduleConfigFiles(path string) []string {
	modConfigFiles := []string{
		"package.json",
		"tsconfig.json",

		// Workspaces files
		"pnpm-workspace.yaml",
		".yarnrc.yml",

		// Lockfiles to include
		"package-lock.json",
		"yarn.lock",
		"pnpm-lock.yaml",
		"bun.lockb",
	}

	for i, file := range modConfigFiles {
		modConfigFiles[i] = filepath.Join(path, file)
	}

	return modConfigFiles
}

// Base returns a Node or Bun container with cache setup for node package managers or bun
func (t *TypescriptSdk) Base() (*dagger.Container, error) {
	ctr := dag.Container().From(t.moduleConfig.image)

	runtime := t.moduleConfig.runtime
	version := t.moduleConfig.runtimeVersion

	switch runtime {
	case Bun:
		return ctr.
			WithoutEntrypoint().
			WithMountedCache("/root/.bun/install/cache", dag.CacheVolume(fmt.Sprintf("mod-bun-cache-%s", tsdistconsts.DefaultBunVersion)), dagger.ContainerWithMountedCacheOpts{
				Sharing: dagger.CacheSharingModePrivate,
			}), nil
	case Node:
		return ctr.
			WithoutEntrypoint().
			// Install default CA certificates and configure node to use them instead of its compiled in CA bundle.
			// This enables use of custom CA certificates if configured in the dagger engine.
			WithExec([]string{"apk", "add", "ca-certificates"}).
			WithEnvVariable("NODE_OPTIONS", "--use-openssl-ca").
			// Add cache volumes for npm, yarn and pnpm
			WithMountedCache("/root/.npm", dag.CacheVolume(fmt.Sprintf("npm-cache-%s-%s", runtime, version))).
			WithMountedCache("/root/.cache/yarn", dag.CacheVolume(fmt.Sprintf("yarn-cache-%s-%s", runtime, version))).
			WithMountedCache("/root/.pnpm-store", dag.CacheVolume(fmt.Sprintf("pnpm-cache-%s-%s", runtime, version))).
			// install tsx from its bundled location in the engine image
			WithMountedDirectory("/usr/local/lib/node_modules/tsx", t.SDKSourceDir.Directory("/tsx_module")).
			WithExec([]string{"ln", "-s", "/usr/local/lib/node_modules/tsx/dist/cli.mjs", "/usr/local/bin/tsx"}), nil

	default:
		return nil, fmt.Errorf("unknown runtime: %s", runtime)
	}
}

// addTemplate adds the template files to the user's module if there's no
// source files in the src directory.
func (t *TypescriptSdk) addTemplate(ctx context.Context, ctr *dagger.Container) (*dagger.Container, error) {
	name := t.moduleConfig.name

	moduleFiles, err := ctr.Directory(".").Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not list module source entries: %w", err)
	}

	// Check if there's a src directory and creates an empty directory if it doesn't exist.
	if !slices.Contains(moduleFiles, "src") {
		ctr = ctr.WithDirectory("src", dag.Directory())
	}

	// Get the list of files in the src directory.
	moduleSourceFiles, err := ctr.Directory("src").Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not list module source entries: %w", err)
	}

	// Check if there's a src directory with .ts files in it.
	// If not, add the template file and replace QuickStart with the module name
	if !slices.ContainsFunc(moduleSourceFiles, func(s string) bool {
		return path.Ext(s) == ".ts"
	}) {
		return ctr.
			WithDirectory("src", ctr.Directory("/opt/module/template/src"), dagger.ContainerWithDirectoryOpts{Include: []string{"*.ts"}}).
			WithExec([]string{"sed", "-i", "-e", fmt.Sprintf("s/QuickStart/%s/g", strcase.ToCamel(name)), "src/index.ts"}), nil
	}

	return ctr, nil
}

// setupModule configure the user's module.
//
// If the user's module has a package.json file, it will run the
// __tsconfig.updator.ts script in order to add dagger to the tsconfig path so
// the editor can give type hints and auto completion.
// Otherwise, it will copy the template config files into the user's module directory.
//
// If there's no src directory or no typescript files in it, it will create one
// and copy the template index.ts file in it.
func (t *TypescriptSdk) configureModule(ctr *dagger.Container) *dagger.Container {
	runtime := t.moduleConfig.runtime

	// If there's a package.json, run the tsconfig updator script and install the genDir.
	// else, copy the template config files.
	if t.moduleConfig.packageJSONConfig != nil {
		if runtime == Bun {
			ctr = ctr.
				WithExec([]string{"bun", "/opt/module/bin/__tsconfig.updator.ts"}).
				WithExec([]string{"bun", "install", "--no-verify", "--no-progress", "--summary", "./sdk"})
		} else {
			ctr = ctr.
				WithExec([]string{"tsx", "/opt/module/bin/__tsconfig.updator.ts"}).
				WithExec([]string{"npm", "pkg", "set", "type=module"}).
				WithExec([]string{"npm", "pkg", "set", "dependencies[@dagger.io/dagger]=./sdk"})
		}
	} else {
		ctr = ctr.WithDirectory(".", ctr.Directory("/opt/module/template"), dagger.ContainerWithDirectoryOpts{Include: []string{"*.json"}})
	}

	return ctr
}

// addSDK returns a directory with the SDK sources.
func (t *TypescriptSdk) addSDK() *dagger.Directory {
	return t.SDKSourceDir.
		WithoutDirectory("codegen").
		WithoutDirectory("runtime").
		WithoutDirectory("tsx_module").
		WithoutDirectory("src/provisioning")
}

// generateClient uses the given container to generate the client code.
func (t *TypescriptSdk) generateClient(ctr *dagger.Container, introspectionJSON *dagger.File) *dagger.Directory {
	return ctr.
		// Add dagger codegen binary.
		WithMountedFile(codegenBinPath, t.SDKSourceDir.File("/codegen")).
		// Mount the introspection file.
		WithMountedFile(schemaPath, introspectionJSON).
		// Execute the code generator using the given introspection file.
		WithExec([]string{
			codegenBinPath,
			"--lang", "typescript",
			"--output", ModSourceDirPath,
			"--module-name", t.moduleConfig.name,
			"--module-source-path", t.moduleConfig.modulePath(),
			"--introspection-json-path", schemaPath,
		}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		// Return the generated code directory.
		Directory(t.moduleConfig.sdkPath())
}

// detectBaseImageRef return the base image ref of the runtime
// based on the user's module config.
//
// If set in the `dagger.baseImage` field of the module's package.json, the
// runtime use that ref.
// If not set, it return the default base image ref based on the detected runtime and
// it's version.
//
// Note: This function must be called after `detectRuntime`.
func (t *TypescriptSdk) detectBaseImageRef() (string, error) {
	runtime := t.moduleConfig.runtime
	version := t.moduleConfig.runtimeVersion

	if t.moduleConfig.packageJSONConfig != nil && t.moduleConfig.packageJSONConfig.Dagger != nil {
		value := t.moduleConfig.packageJSONConfig.Dagger.BaseImage
		if value != "" {
			return value, nil
		}
	}

	switch runtime {
	case Bun:
		if version != "" {
			return fmt.Sprintf("oven/%s:%s-alpine", Bun, version), nil
		}

		return tsdistconsts.DefaultBunImageRef, nil
	case Node:
		if version != "" {
			return fmt.Sprintf("%s:%s-alpine", Node, version), nil
		}

		return tsdistconsts.DefaultNodeImageRef, nil
	default:
		return "", fmt.Errorf("unknown runtime: %q", runtime)
	}
}

// DetectRuntime returns the runtime(bun or node) detected for the user's module
// If a runtime is specfied inside the package.json, it will be used.
// If a package-lock.json, yarn.lock, or pnpm-lock.yaml is present, node will be used.
// If a bun.lockb is present, bun will be used.
// If none of the above is present, node will be used.
//
// If the runtime is detected and pinned to a specific version, it will also return the pinned version.
func (t *TypescriptSdk) detectRuntime() error {
	// If we find a package.json, we check if the runtime is specified in `dagger.runtime` field.
	if t.moduleConfig.packageJSONConfig != nil && t.moduleConfig.packageJSONConfig.Dagger != nil {
		value := t.moduleConfig.packageJSONConfig.Dagger.Runtime
		if value != "" {
			// Retrieve the runtime and version from the value (e.g., node@lts, bun@1)
			// If version isn't specified, version will be an empty string and only the runtime will be used in Base.
			runtime, version, _ := strings.Cut(value, "@")

			switch runtime := SupportedTSRuntime(runtime); runtime {
			case Bun, Node:
				t.moduleConfig.runtime = runtime
				t.moduleConfig.runtimeVersion = version

				return nil
			default:
				return fmt.Errorf("unsupported runtime %q", runtime)
			}
		}
	}

	// Try to detect runtime from lock files
	if t.moduleConfig.hasFile("bun.lockb") {
		t.moduleConfig.runtime = Bun

		return nil
	}

	if t.moduleConfig.hasFile("package-lock.json") ||
		t.moduleConfig.hasFile("yarn.lock") ||
		t.moduleConfig.hasFile("pnpm-lock.yaml") {
		t.moduleConfig.runtime = Node

		return nil
	}

	return nil
}

// detectPackageManager detects the package manager from the user's module.
// If the package.json file has a field "packageManager", it will use that to
// determine the package manager to use. Otherwise, it will use the default
// package manager based on the lock files present in the module.
//
// If none of the above works, it will use yarn.
//
// Except if the package.json has an invalid value in field "packageManager", this
// function should never return an error.
func (t *TypescriptSdk) detectPackageManager() (SupportedPackageManager, string, error) {
	// If the runtime is Bun, we should use BunManager
	if t.moduleConfig.runtime == Bun {
		return BunManager, "", nil
	}

	// If we find a package.json, we check if the packageManager is specified in `packageManager` field.
	if t.moduleConfig.packageJSONConfig != nil {
		value := t.moduleConfig.packageJSONConfig.PackageManager
		if value != "" {
			// Retrieve the package manager and version from the value (e.g., yarn@4.2.0, pnpm@8.5.1)
			packageManager, version, _ := strings.Cut(value, "@")

			if version == "" {
				return "", "", fmt.Errorf("packageManager version is missing, please add it to your package.json")
			}

			switch SupportedPackageManager(packageManager) {
			case Yarn, Pnpm, Npm:
				return SupportedPackageManager(packageManager), version, nil
			default:
				return "", "", fmt.Errorf("detected unknown package manager: %s", packageManager)
			}
		}
	}

	if t.moduleConfig.hasFile("bun.lockb") {
		return BunManager, "", nil
	}

	if t.moduleConfig.hasFile("package-lock.json") {
		return Npm, NpmDefaultVersion, nil
	}

	if t.moduleConfig.hasFile("yarn.lock") {
		return Yarn, YarnDefaultVersion, nil
	}

	if t.moduleConfig.hasFile("pnpm-lock.yaml") {
		return Pnpm, PnpmDefaultVersion, nil
	}

	// Default to yarn
	return Yarn, YarnDefaultVersion, nil
}

// generateLockFile generate a lock file for the matching package manager.
func (t *TypescriptSdk) generateLockFile(ctr *dagger.Container) (*dagger.Container, error) {
	packageManager := t.moduleConfig.packageManager
	version := t.moduleConfig.packageManagerVersion

	switch packageManager {
	case Yarn:
		// Enable corepack
		// NOTE: this incidentally fetches and installs node modules, maybe we could install yarn some other way?
		ctr = ctr.
			WithExec([]string{"corepack", "enable"}).
			WithExec([]string{"corepack", "use", fmt.Sprintf("yarn@%s", version)})

		// Install dependencies and extract the lockfile
		file := ctr.
			WithExec([]string{"yarn", "install", "--mode", "update-lockfile"}).File("yarn.lock")

		// We use node-modules linker for yarn >= v3 because it's not working with pnp.
		if semver.Compare(fmt.Sprintf("v%s", t.moduleConfig.packageManagerVersion), "v3.0.0") >= 0 {
			ctr = ctr.WithNewFile(".yarnrc.yml", `nodeLinker: node-modules`)
		}

		// Sadly, yarn < v3 doesn't support generating a lockfile without installing the dependencies.
		// So we use npm to generate the lockfile and then import it into yarn.
		return ctr.WithFile("yarn.lock", file), nil
	case Pnpm:
		ctr = ctr.WithExec([]string{"npm", "install", "-g", fmt.Sprintf("pnpm@%s", version)})

		if !t.moduleConfig.hasFile("pnpm-workspace.yaml") {
			ctr = ctr.
				WithNewFile("pnpm-workspace.yaml", `packages:
  - './sdk'
			`)
		}

		return ctr.WithExec([]string{"pnpm", "install", "--lockfile-only"}), nil
	case Npm:
		return ctr.
			WithExec([]string{"npm", "install", "-g", fmt.Sprintf("npm@%s", version)}).
			WithExec([]string{"npm", "install", "--package-lock-only"}), nil
	case BunManager:
		return ctr.
			WithExec([]string{"bun", "install", "--no-verify", "--no-progress"}), nil
	default:
		return nil, fmt.Errorf("detected unknown package manager: %s", packageManager)
	}
}

// installDependencies installs the dependencies using the detected package manager.
func (t *TypescriptSdk) installDependencies(ctr *dagger.Container) (*dagger.Container, error) {
	switch t.moduleConfig.packageManager {
	case Yarn:
		if semver.Compare(fmt.Sprintf("v%s", t.moduleConfig.packageManagerVersion), "v3.0.0") <= 0 {
			return ctr.
				WithExec([]string{"yarn", "install", "--frozen-lockfile"}), nil
		}

		return ctr.WithExec([]string{"yarn", "install", "--immutable"}), nil
	case Pnpm:
		return ctr.
			WithExec([]string{"pnpm", "install", "--frozen-lockfile", "--shamefully-hoist=true"}), nil
	case Npm:
		return ctr.
			WithExec([]string{"npm", "ci"}), nil
	case BunManager:
		return ctr.
			WithExec([]string{"bun", "install", "--no-verify", "--no-progress"}), nil
	default:
		return nil, fmt.Errorf("detected unknown package manager: %s", t.moduleConfig.packageManager)
	}
}

// analyzeModuleConfig analyzes the module config and populates the moduleConfig field.
//
// It detect the module name, source subpath, runtime, package manager and their versions.
// It also populates the moduleConfig.entries map with the list of files present in the module source.
//
// It's a utility function that should be called before calling any other exposed function in this module.
func (t *TypescriptSdk) analyzeModuleConfig(ctx context.Context, modSource *dagger.ModuleSource) (err error) {
	if t.moduleConfig == nil {
		t.moduleConfig = &moduleConfig{
			entries:           make(map[string]bool),
			runtime:           Node,
			packageJSONConfig: nil,
		}
	}

	t.moduleConfig.name, err = modSource.ModuleOriginalName(ctx)
	if err != nil {
		return fmt.Errorf("could not load module config name: %w", err)
	}

	t.moduleConfig.subPath, err = modSource.SourceSubpath(ctx)
	if err != nil {
		return fmt.Errorf("could not load module config source subpath: %w", err)
	}

	// If a first init, there will be no directory, so we ignore the error here.
	// We also only include package.json & lockfiles to benefit from caching.
	t.moduleConfig.source = modSource.ContextDirectory().Directory(t.moduleConfig.subPath)
	configEntries, err := dag.Directory().WithDirectory(".", t.moduleConfig.source, dagger.DirectoryWithDirectoryOpts{
		Include: t.moduleConfigFiles("."),
	}).Entries(ctx)
	if err == nil {
		for _, entry := range configEntries {
			t.moduleConfig.entries[entry] = true
		}
	}

	if t.moduleConfig.hasFile("package.json") {
		var packageJSONConfig packageJSONConfig

		content, err := t.moduleConfig.source.File("package.json").Contents(ctx)
		if err != nil {
			return fmt.Errorf("failed to read package.json: %w", err)
		}

		if err := json.Unmarshal([]byte(content), &packageJSONConfig); err != nil {
			return fmt.Errorf("failed to unmarshal package.json: %w", err)
		}

		t.moduleConfig.packageJSONConfig = &packageJSONConfig
	}

	if err := t.detectRuntime(); err != nil {
		return fmt.Errorf("failed to detect module runtime: %w", err)
	}

	t.moduleConfig.packageManager, t.moduleConfig.packageManagerVersion, err = t.detectPackageManager()
	if err != nil {
		return fmt.Errorf("failed to detect package manager: %w", err)
	}

	t.moduleConfig.image, err = t.detectBaseImageRef()
	if err != nil {
		return fmt.Errorf("failed to detect base image ref: %w", err)
	}

	return nil
}

// Return true if the file is present in the module source.
func (c *moduleConfig) hasFile(name string) bool {
	_, ok := c.entries[name]

	return ok
}

func (c *moduleConfig) modulePath() string {
	return filepath.Join(ModSourceDirPath, c.subPath)
}

func (c *moduleConfig) sdkPath() string {
	return filepath.Join(c.modulePath(), GenDir)
}

func (c *moduleConfig) entrypointPath() string {
	return filepath.Join(ModSourceDirPath, c.subPath, SrcDir, EntrypointExecutableFile)
}

func (c *moduleConfig) tsConfigPath() string {
	return filepath.Join(ModSourceDirPath, c.subPath, "tsconfig.json")
}
