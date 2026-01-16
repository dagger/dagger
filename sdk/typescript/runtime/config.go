package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsdistconsts"
)

type SupportedTSRuntime string

const (
	Bun  SupportedTSRuntime = "bun"
	Node SupportedTSRuntime = "node"
	Deno SupportedTSRuntime = "deno"
)

type SupportedPackageManager string

const (
	Yarn        SupportedPackageManager = "yarn"
	Pnpm        SupportedPackageManager = "pnpm"
	Npm         SupportedPackageManager = "npm"
	BunManager  SupportedPackageManager = "bun"
	DenoManager SupportedPackageManager = "deno"
)

const (
	PnpmDefaultVersion = "8.15.4"
	YarnDefaultVersion = "1.22.22"

	// NOTE: when changing this version, check if the `DefaultNodeVersion` var in sdk/typescript/runtime/tsdistconsts/consts.go
	// should be updated to an image that has the same version of npm pre-installed in the container
	NpmDefaultVersion = "10.9.0"
)

type SDKLibOrigin string

const (
	Bundle SDKLibOrigin = "bundle"
	Local  SDKLibOrigin = "local"
	Remote SDKLibOrigin = "remote"
)

type packageJSONConfig struct {
	PackageManager string            `json:"packageManager"`
	Dependencies   map[string]string `json:"dependencies"`
	Dagger         *struct {
		BaseImage string `json:"baseImage"`
		Runtime   string `json:"runtime"`
	} `json:"dagger"`
}

type denoJSONConfig struct {
	Workspace []string          `json:"workspace"`
	Imports   map[string]string `json:"imports"`
	Dagger    *struct {
		BaseImage string `json:"baseImage"`
	} `json:"dagger"`
}

type moduleConfig struct {
	// Runtime configuration
	runtime        SupportedTSRuntime
	runtimeVersion string

	// Custom base image
	image             string
	packageJSONConfig *packageJSONConfig
	denoJSONConfig    *denoJSONConfig

	// Package manager configuration
	packageManager        SupportedPackageManager
	packageManagerVersion string

	// Files in the user's module
	contextDirectory *dagger.Directory
	source           *dagger.Directory
	entries          map[string]bool

	// Module config
	name    string
	subPath string
	modPath string
	sdk     string
	debug   bool

	// Location of the SDK library
	sdkLibOrigin SDKLibOrigin
}

// analyzeModuleConfig analyzes the module config and populates the moduleConfig field.
//
// It detects the module name, source subpath, runtime, package manager and their versions.
// It also populates the moduleConfig.entries map with the list of files present in the module source.
//
// It's a utility function that should be called before calling any other exposed function in this module.
func analyzeModuleConfig(ctx context.Context, modSource *dagger.ModuleSource) (cfg *moduleConfig, err error) {
	cfg = &moduleConfig{
		entries:           make(map[string]bool),
		contextDirectory:  modSource.ContextDirectory(),
		runtime:           Node,
		packageJSONConfig: nil,
		sdkLibOrigin:      Bundle,
		source:            dag.Directory(),
	}

	cfg.name, err = modSource.ModuleOriginalName(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config name: %w", err)
	}

	cfg.subPath, err = modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config source subpath: %w", err)
	}

	cfg.modPath, err = modSource.SourceRootSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config source root subpath: %w", err)
	}

	// We retrieve the SDK because if it's set, that means the module is implementing
	// logic and is not just for a standalone client.
	cfg.sdk, err = modSource.SDK().Source(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config sdk: %w", err)
	}

	cfg.debug, err = modSource.SDK().Debug(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config sdk debug: %w", err)
	}

	// If a first init, there will be no directory, so we ignore the error here.
	// We also only include package.json & lockfiles to benefit from caching.
	// If there's no source yet, we keep it as an empty directory.
	_, silentErr := modSource.ContextDirectory().Directory(cfg.subPath).Entries(ctx)
	if silentErr == nil {
		cfg.source = modSource.ContextDirectory().Directory(cfg.subPath)
	}

	configEntries, err := dag.Directory().
		WithDirectory(".", cfg.source, dagger.DirectoryWithDirectoryOpts{
			Include: moduleConfigFiles("."),
		}).Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list module source entries: %w", err)
	}

	for _, entry := range configEntries {
		cfg.entries[entry] = true
	}

	if cfg.hasFile("package.json") {
		var packageJSONConfig packageJSONConfig

		content, err := cfg.source.File("package.json").Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read package.json: %w", err)
		}

		if err := json.Unmarshal([]byte(content), &packageJSONConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal package.json: %w", err)
		}

		cfg.packageJSONConfig = &packageJSONConfig
	}

	if cfg.hasFile("deno.json") {
		var denoJSONConfig denoJSONConfig

		content, err := cfg.source.File("deno.json").Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read deno.json: %w", err)
		}

		if err := json.Unmarshal([]byte(content), &denoJSONConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal deno.json: %w", err)
		}

		cfg.denoJSONConfig = &denoJSONConfig
	}

	cfg.runtime, cfg.runtimeVersion, err = cfg.detectRuntime()
	if err != nil {
		return nil, fmt.Errorf("failed to detect module runtime: %w", err)
	}

	cfg.packageManager, cfg.packageManagerVersion, err = cfg.detectPackageManager()
	if err != nil {
		return nil, fmt.Errorf("failed to detect package manager: %w", err)
	}

	cfg.image, err = cfg.detectBaseImageRef()
	if err != nil {
		return nil, fmt.Errorf("failed to detect base image ref: %w", err)
	}

	cfg.sdkLibOrigin, err = cfg.detectSDKLibOrigin()
	if err != nil {
		return nil, fmt.Errorf("failed to detect sdk lib origin: %w", err)
	}

	return cfg, nil
}

// analyzeClientConfig is a simpler version of analyzeModuleConfig to fetch all the information
// required to generate a client.
// This include: the module root path, the runtime, the package manager, the sdk lib origin
// and if the module also has a SDK.
func analyzeClientConfig(ctx context.Context, modSource *dagger.ModuleSource) (cfg *moduleConfig, err error) {
	cfg = &moduleConfig{
		entries:           make(map[string]bool),
		contextDirectory:  modSource.ContextDirectory(),
		runtime:           Node,
		packageJSONConfig: nil,
		sdkLibOrigin:      Bundle,
		source:            dag.Directory(),
	}

	cfg.subPath, err = modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config source subpath: %w", err)
	}

	cfg.modPath, err = modSource.SourceRootSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config source root subpath: %w", err)
	}

	// We retrieve the SDK because if it's set, that means the module is implementing
	// logic and is not just for a standalone client.
	cfg.sdk, err = modSource.SDK().Source(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config sdk: %w", err)
	}

	// We are using the module path as source here since the client configuration should
	// be aside the dagger.json
	_, silentErr := modSource.ContextDirectory().Directory(cfg.modPath).Entries(ctx)
	if silentErr == nil {
		cfg.source = modSource.ContextDirectory().Directory(cfg.modPath)
	}

	configEntries, err := dag.Directory().
		WithDirectory(".", cfg.source, dagger.DirectoryWithDirectoryOpts{
			Include: moduleConfigFiles("."),
		}).Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list module source entries: %w", err)
	}

	for _, entry := range configEntries {
		cfg.entries[entry] = true
	}

	if cfg.hasFile("package.json") {
		var packageJSONConfig packageJSONConfig

		content, err := cfg.source.File("package.json").Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read package.json: %w", err)
		}

		if err := json.Unmarshal([]byte(content), &packageJSONConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal package.json: %w", err)
		}

		cfg.packageJSONConfig = &packageJSONConfig
	}

	if cfg.hasFile("deno.json") {
		var denoJSONConfig denoJSONConfig

		content, err := cfg.source.File("deno.json").Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read deno.json: %w", err)
		}

		if err := json.Unmarshal([]byte(content), &denoJSONConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal deno.json: %w", err)
		}

		cfg.denoJSONConfig = &denoJSONConfig
	}

	cfg.runtime, cfg.runtimeVersion, err = cfg.detectRuntime()
	if err != nil {
		return nil, fmt.Errorf("failed to detect module runtime: %w", err)
	}

	cfg.sdkLibOrigin, err = cfg.detectSDKLibOrigin()
	if err != nil {
		return nil, fmt.Errorf("failed to detect sdk lib origin: %w", err)
	}

	return cfg, err
}

// detectSDKLibOrigin return the SDK library config based on the user's module config.
// For Node & Bun:
// - if there's no package.json -> default to Bundle.
// - if the package.json has `dependencies[@dagger.io/dagger]=./sdk` -> Return Local.
// - if the package.json has `dependencies[@dagger.io/dagger] = <version>` -> Return Remote.
// - if the package.json has no `dependencies[@dagger.io/dagger]` -> Return Bundle.
// For Deno:
// - if there's no deno.json -> default to Bundle.
// - if deno.json has `workspaces` with `./sdk` in it -> Return Local
// - if deno.json has `imports` with `@dagger.io/dagger="npm:@dagger.io/dagger@<version>` -> Return Remote
// - if deno.json has no `imports` with  `@dagger.io/dagger` -> Return Bundle
//
// Return Bundle by default since it's more performant.
func (c *moduleConfig) detectSDKLibOrigin() (SDKLibOrigin, error) {
	runtime := c.runtime

	switch runtime {
	case Node, Bun:
		if c.packageJSONConfig == nil {
			return Bundle, nil
		}

		daggerDep, exist := c.packageJSONConfig.Dependencies["@dagger.io/dagger"]
		if !exist {
			return Bundle, nil
		}

		if daggerDep == "./sdk" {
			return Local, nil
		}

		return Remote, nil
	case Deno:
		if c.denoJSONConfig == nil {
			return Bundle, nil
		}

		if slices.Contains(c.denoJSONConfig.Workspace, "./sdk") {
			return Local, nil
		}

		daggerDep, exist := c.denoJSONConfig.Imports["@dagger.io/dagger"]
		if !exist {
			return Bundle, nil
		}

		if strings.HasPrefix(daggerDep, "npm:@dagger.io/dagger") {
			return Remote, nil
		}

		// dagger.io/dagger is imported but points to the local SDK directory so it must be local.
		if strings.HasPrefix(daggerDep, "./sdk/src") {
			return Local, nil
		}

		// Otherwise, it's imported but point to ./sdk/index.ts so it's bundled.
		return Bundle, nil
	default:
		return Bundle, fmt.Errorf("unknown runtime: %s", runtime)
	}
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
func (c *moduleConfig) detectBaseImageRef() (string, error) {
	runtime := c.runtime
	version := c.runtimeVersion

	if c.packageJSONConfig != nil && c.packageJSONConfig.Dagger != nil {
		value := c.packageJSONConfig.Dagger.BaseImage
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
	case Deno:
		if c.denoJSONConfig != nil && c.denoJSONConfig.Dagger != nil {
			value := c.denoJSONConfig.Dagger.BaseImage
			if value != "" {
				return value, nil
			}
		}

		return tsdistconsts.DefaultDenoImageRef, nil
	default:
		return "", fmt.Errorf("unknown runtime: %q", runtime)
	}
}

// DetectRuntime returns the runtime(bun or node) detected for the user's module
// If a runtime is specfied inside the package.json, it will be used.
// If a package-lock.json, yarn.lock, or pnpm-lock.yaml is present, node will be used.
// If a bun.lock or bun.lockb is present, bun will be used.
// If none of the above is present, node will be used.
//
// If the runtime is detected and pinned to a specific version, it will also return the pinned version.
func (c *moduleConfig) detectRuntime() (SupportedTSRuntime, string, error) {
	// If we find a package.json, we check if the runtime is specified in `dagger.runtime` field.
	if c.packageJSONConfig != nil && c.packageJSONConfig.Dagger != nil {
		value := c.packageJSONConfig.Dagger.Runtime
		if value != "" {
			// Retrieve the runtime and version from the value (e.g., node@lts, bun@1)
			// If version isn't specified, version will be an empty string and only the runtime will be used in Base.
			runtime, version, _ := strings.Cut(value, "@")

			switch runtime := SupportedTSRuntime(runtime); runtime {
			case Bun, Node:
				return runtime, version, nil
			default:
				return "", "", fmt.Errorf("unsupported runtime %q", runtime)
			}
		}
	}

	// Try to detect runtime from lock files
	if c.hasFile("bun.lockb") || c.hasFile("bun.lock") {
		return Bun, "", nil
	}

	if c.hasFile("package-lock.json") ||
		c.hasFile("yarn.lock") ||
		c.hasFile("pnpm-lock.yaml") {
		return Node, "", nil
	}

	if c.hasFile("deno.json") ||
		c.hasFile("deno.lock") {
		return Deno, "", nil
	}

	return Node, "", nil
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
func (c *moduleConfig) detectPackageManager() (SupportedPackageManager, string, error) {
	// If the runtime is Bun, we should use BunManager
	if c.runtime == Bun {
		return BunManager, "", nil
	}

	// If the runtime is deno, we should use the DenoManager
	if c.runtime == Deno {
		return DenoManager, "", nil
	}

	// If we find a package.json, we check if the packageManager is specified in `packageManager` field.
	if c.packageJSONConfig != nil {
		value := c.packageJSONConfig.PackageManager
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

	// Bun can additionally output a yarn.lock file, so we need to check for Bun before Yarn.
	if c.hasFile("bun.lockb") || c.hasFile("bun.lock") {
		return BunManager, "", nil
	}

	if c.hasFile("package-lock.json") {
		return Npm, NpmDefaultVersion, nil
	}

	if c.hasFile("yarn.lock") {
		return Yarn, YarnDefaultVersion, nil
	}

	if c.hasFile("pnpm-lock.yaml") {
		return Pnpm, PnpmDefaultVersion, nil
	}

	// Default to yarn
	return Yarn, YarnDefaultVersion, nil
}

// Return true if the file is present in the module source.
func (c *moduleConfig) hasFile(name string) bool {
	_, ok := c.entries[name]

	return ok
}

// Return the path to the module source.
func (c *moduleConfig) modulePath() string {
	return filepath.Join(ModSourceDirPath, c.subPath)
}

// Return the path to the tsconfig.json file inside the module source.
func (c *moduleConfig) tsConfigPath() string {
	return filepath.Join(ModSourceDirPath, c.subPath, "tsconfig.json")
}

// Get the source code directory wrapped in a dagger.Directory
// to be used in the runtime container.
// This excludes config files, sdk and node_modules but keep any
// extra files that may exist in the module source dir.
func (c *moduleConfig) wrappedSourceCodeDirectory() *dagger.Directory {
	return dag.Directory().WithDirectory("/",
		c.source,
		dagger.DirectoryWithDirectoryOpts{
			Exclude: append(
				moduleConfigFiles("."),
				"sdk",
				"node_modules",
			),
		},
	)
}

func (c *moduleConfig) libGeneratorOpts() *LibGeneratorOpts {
	return &LibGeneratorOpts{
		moduleName: c.name,
		modulePath: c.modulePath(),
	}
}

// Returns a list of files to include for module configs.
func moduleConfigFiles(path string) []string {
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

		// Bun
		"bun.lockb",
		"bun.lock",
		"bunfig.toml",

		// Deno
		"deno.json",
		"deno.lock",
	}

	for i, file := range modConfigFiles {
		modConfigFiles[i] = filepath.Join(path, file)
	}

	return modConfigFiles
}
