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
	NpmDefaultVersion  = "10.7.0"
)

type SDKLibOrigin string

const (
	Bundle SDKLibOrigin = "bundle"
	Local  SDKLibOrigin = "local"
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
	Workspaces []string `json:"workspaces"`
	Dagger     *struct {
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

	// Location of the SDK library
	sdkLibOrigin SDKLibOrigin
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
			contextDirectory:  modSource.ContextDirectory(),
			runtime:           Node,
			packageJSONConfig: nil,
			sdkLibOrigin:      Bundle,
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

	if t.moduleConfig.hasFile("deno.json") {
		var denoJSONConfig denoJSONConfig

		content, err := t.moduleConfig.source.File("deno.json").Contents(ctx)
		if err != nil {
			return fmt.Errorf("failed to read deno.json: %w", err)
		}

		if err := json.Unmarshal([]byte(content), &denoJSONConfig); err != nil {
			return fmt.Errorf("failed to unmarshal deno.json: %w", err)
		}

		t.moduleConfig.denoJSONConfig = &denoJSONConfig
	}

	t.moduleConfig.runtime, t.moduleConfig.runtimeVersion, err = t.detectRuntime()
	if err != nil {
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

	t.moduleConfig.sdkLibOrigin, err = t.detectSDKLibOrigin()
	if err != nil {
		return fmt.Errorf("failed to detect sdk lib origin: %w", err)
	}

	return nil
}

// detectSDKLibOrigin return the SDK library config based on the user's module config.
// For Node & Bun:
// - if the package.json has `dependencies[@dagger.io/dagger]=./sdk` -> Return Local
// - else -> Return Bundle
// For Deno:
// - if deno.json has `workspaces` with `./sdk` in it -> Return Local
// - else -> Return Bundle
// Return Bundle by default since it's more performant.
func (t *TypescriptSdk) detectSDKLibOrigin() (SDKLibOrigin, error) {
	runtime := t.moduleConfig.runtime

	switch runtime {
	case Node, Bun:
		if t.moduleConfig.packageJSONConfig != nil && t.moduleConfig.packageJSONConfig.Dependencies["@dagger.io/dagger"] == "./sdk" {
			return Local, nil
		}

		return Bundle, nil
	case Deno:
		if t.moduleConfig.denoJSONConfig != nil && slices.Contains(t.moduleConfig.denoJSONConfig.Workspaces, "./sdk") {
			return Local, nil
		}

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
	case Deno:
		if t.moduleConfig.denoJSONConfig != nil && t.moduleConfig.denoJSONConfig.Dagger != nil {
			value := t.moduleConfig.denoJSONConfig.Dagger.BaseImage
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
func (t *TypescriptSdk) detectRuntime() (SupportedTSRuntime, string, error) {
	// If we find a package.json, we check if the runtime is specified in `dagger.runtime` field.
	if t.moduleConfig.packageJSONConfig != nil && t.moduleConfig.packageJSONConfig.Dagger != nil {
		value := t.moduleConfig.packageJSONConfig.Dagger.Runtime
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
	if t.moduleConfig.hasFile("bun.lockb") || t.moduleConfig.hasFile("bun.lock") {
		return Bun, "", nil
	}

	if t.moduleConfig.hasFile("package-lock.json") ||
		t.moduleConfig.hasFile("yarn.lock") ||
		t.moduleConfig.hasFile("pnpm-lock.yaml") {
		return Node, "", nil
	}

	if t.moduleConfig.hasFile("deno.json") ||
		t.moduleConfig.hasFile("deno.lock") {
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
func (t *TypescriptSdk) detectPackageManager() (SupportedPackageManager, string, error) {
	// If the runtime is Bun, we should use BunManager
	if t.moduleConfig.runtime == Bun {
		return BunManager, "", nil
	}

	// If the runtime is deno, we should use the DenoManager
	if t.moduleConfig.runtime == Deno {
		return DenoManager, "", nil
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

	// Bun can additionally output a yarn.lock file, so we need to check for Bun before Yarn.
	if t.moduleConfig.hasFile("bun.lockb") || t.moduleConfig.hasFile("bun.lock") {
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

// Return true if the file is present in the module source.
func (c *moduleConfig) hasFile(name string) bool {
	_, ok := c.entries[name]

	return ok
}

// Return the path to the module source.
func (c *moduleConfig) modulePath() string {
	return filepath.Join(ModSourceDirPath, c.subPath)
}

// Return the path to the SDK directory inside the module source.
func (c *moduleConfig) sdkPath() string {
	return filepath.Join(c.modulePath(), GenDir)
}

// Return the path to the entrypoint file inside the module source.
func (c *moduleConfig) entrypointPath() string {
	return filepath.Join(ModSourceDirPath, c.subPath, SrcDir, EntrypointExecutableFile)
}

// Return the path to the tsconfig.json file inside the module source.
func (c *moduleConfig) tsConfigPath() string {
	return filepath.Join(ModSourceDirPath, c.subPath, "tsconfig.json")
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
		"bun.lock",

		// Deno
		"deno.json",
		"deno.lock",
	}

	for i, file := range modConfigFiles {
		modConfigFiles[i] = filepath.Join(path, file)
	}

	return modConfigFiles
}
