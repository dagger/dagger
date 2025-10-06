package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsdistconsts"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/iancoleman/strcase"
	"golang.org/x/mod/semver"
)

type moduleRuntimeContainer struct {
	sdkSourceDir *dagger.Directory
	cfg          *moduleConfig
	ctr          *dagger.Container
}

func (m *moduleRuntimeContainer) Container() *dagger.Container {
	return m.ctr
}

func (m *moduleRuntimeContainer) ModuleDirectory() *dagger.Directory {
	return m.ctr.Directory(ModSourceDirPath)
}

// Base returns a Node, Bun or Deno base container with utilities and cache
// setup.
func runtimeBaseContainer(cfg *moduleConfig, sdkSourceDir *dagger.Directory) *moduleRuntimeContainer {
	modRuntimeCtr := &moduleRuntimeContainer{
		sdkSourceDir: sdkSourceDir,
		cfg:          cfg,
		ctr:          dag.Container().From(cfg.image),
	}

	runtime := cfg.runtime
	version := cfg.runtimeVersion

	switch runtime {
	case Bun:
		modRuntimeCtr.ctr = modRuntimeCtr.ctr.
			WithoutEntrypoint().
			WithMountedCache("/root/.bun/install/cache", dag.CacheVolume(fmt.Sprintf("mod-bun-cache-%s", tsdistconsts.DefaultBunVersion)), dagger.ContainerWithMountedCacheOpts{
				Sharing: dagger.CacheSharingModePrivate,
			})
	case Deno:
		modRuntimeCtr.ctr = modRuntimeCtr.ctr.
			WithoutEntrypoint().
			WithMountedCache("/root/.deno/cache", dag.CacheVolume(fmt.Sprintf("mod-deno-cache-%s", tsdistconsts.DefaultDenoVersion)))
	case Node:
		modRuntimeCtr.ctr = modRuntimeCtr.ctr.
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
			WithMountedDirectory("/usr/local/lib/node_modules/tsx", modRuntimeCtr.sdkSourceDir.Directory("/tsx_module")).
			WithExec([]string{"ln", "-s", "/usr/local/lib/node_modules/tsx/dist/cli.mjs", "/usr/local/bin/tsx"})
	}

	return modRuntimeCtr
}

// Configure the user's module based on the detected runtime and user's config files.
//
// For Bun and Node:
// - If no `package.json` is present, it will create a default one with a `tsconfig.json`
// using the template directory .
// - If `package.json` is present, it will update the `package.json` with
// correct setup and update the `tsconfig.json` using the `bin/__tsconfig.updator.ts` script.
//
// For Deno:
// - It will update the `deno.json` with correct setup using the `bin/__deno_config_updator.ts` script.
func (m *moduleRuntimeContainer) withConfiguredRuntimeEnvironment() *moduleRuntimeContainer {
	runtime := m.cfg.runtime

	m.ctr = m.ctr.
		WithDirectory(
			ModSourceDirPath,
			m.cfg.contextDirectory,
			dagger.ContainerWithDirectoryOpts{
				Include: moduleConfigFiles(m.cfg.subPath),
			},
		).
		WithWorkdir(m.cfg.modulePath())

	switch runtime {
	case Bun:
		if m.cfg.packageJSONConfig == nil {
			m.ctr = m.ctr.
				WithDirectory(".",
					templateDirectory(),
					dagger.ContainerWithDirectoryOpts{Include: []string{"*.json"}},
				)

			break
		}

		m.ctr = m.ctr.
			WithMountedFile("/opt/module/bin/__tsconfig.updator.ts", tsConfigUpdatorFile()).
			WithExec([]string{"bun", "/opt/module/bin/__tsconfig.updator.ts", fmt.Sprintf("--sdk-lib-origin=%s", m.cfg.sdkLibOrigin)}).
			WithFile("package.json", m.configurePackageJSON(m.ctr.File("package.json")))
	case Node:
		if m.cfg.packageJSONConfig == nil {
			m.ctr = m.ctr.
				WithDirectory(".",
					templateDirectory(),
					dagger.ContainerWithDirectoryOpts{Include: []string{"*.json"}},
				)

			break
		}

		m.ctr = m.ctr.
			WithMountedFile("/opt/module/bin/__tsconfig.updator.ts", tsConfigUpdatorFile()).
			WithExec([]string{"tsx", "/opt/module/bin/__tsconfig.updator.ts", fmt.Sprintf("--sdk-lib-origin=%s", m.cfg.sdkLibOrigin)}).
			WithFile("package.json", m.configurePackageJSON(m.ctr.File("package.json")))
	case Deno:
		m.ctr = m.ctr.
			WithMountedFile("/opt/module/bin/__deno_config_updator.ts", denoConfigUpdatorFile()).
			WithExec([]string{"deno", "run", "-A", "/opt/module/bin/__deno_config_updator.ts", fmt.Sprintf("--sdk-lib-origin=%s", m.cfg.sdkLibOrigin)})
	}

	return m
}

// Update the user's package.json with required dependencies to use the
// Typescript SDK.
// We need to use a node container to run npm since it's not available on
// bun (https://github.com/oven-sh/bun/issues/9840).
// This function can be removed once supported by bun so we
// can gain some performance.
func (m *moduleRuntimeContainer) configurePackageJSON(file *dagger.File) *dagger.File {
	ctr := dag.
		Container().
		From(tsdistconsts.DefaultNodeImageRef).
		WithDirectory("/src", dag.Directory().WithFile("package.json", file)).
		WithWorkdir("/src").
		WithExec([]string{"npm", "pkg", "set", "type=module"})

	if m.cfg.packageJSONConfig != nil {
		_, ok := m.cfg.packageJSONConfig.Dependencies["typescript"]
		if !ok {
			ctr = ctr.WithExec([]string{"npm", "pkg", "set", "dependencies.typescript=^5.5.4"})
		}
	}

	return ctr.File("/src/package.json")
}

// Setup the package manager for the user's module.
//
// Yarn:
// - Enable corepack
// - Install yarn using corepack (this fetches and install nodes modules, we should find a better way to install
// yarn when possible)
// Pnpm:
// - Install pnpm required version using npm
// - Setup pnpm workspace if needed to fetch transitive local dependencies
// Npm:
// - Install npm required version using npm
// Bun & Deno:
// - No setup needed, the package manager is already setup when the image is pulled.
func (m *moduleRuntimeContainer) withSetupPackageManager() *moduleRuntimeContainer {
	packageManager := m.cfg.packageManager
	version := m.cfg.packageManagerVersion

	switch packageManager {
	case Yarn:
		m.ctr = m.ctr.
			WithExec([]string{"corepack", "enable"}).
			WithExec([]string{"corepack", "use", fmt.Sprintf("yarn@%s", version)})
	case Pnpm:
		m.ctr = m.ctr.WithExec([]string{"npm", "install", "-g", fmt.Sprintf("pnpm@%s", version)})

		if !m.cfg.hasFile("pnpm-workspace.yaml") && m.cfg.sdkLibOrigin == Local {
			m.ctr = m.ctr.
				WithNewFile("pnpm-workspace.yaml", `packages:
  - './sdk'
			`)
		}
	case Npm:
		m.ctr = m.ctr.
			WithExec([]string{"npm", "install", "-g", fmt.Sprintf("npm@%s", version)})
	}

	return m
}

// Generate a lock file for the matching package manager.
func (m *moduleRuntimeContainer) withGeneratedLockFile() *moduleRuntimeContainer {
	packageManager := m.cfg.packageManager

	switch packageManager {
	case Yarn:
		// Install dependencies and extract the lockfile
		file := m.ctr.
			WithExec([]string{"yarn", "install", "--mode", "update-lockfile"}).File("yarn.lock")

		// We use node-modules linker for yarn >= v3 because it's not working with pnp.
		if semver.Compare(fmt.Sprintf("v%s", m.cfg.packageManagerVersion), "v3.0.0") >= 0 {
			m.ctr = m.ctr.WithNewFile(".yarnrc.yml", `nodeLinker: node-modules`)
		}

		// Sadly, yarn < v3 doesn't support generating a lockfile without installing the dependencies.
		// So we use npm to generate the lockfile and then import it into yarn.
		m.ctr = m.ctr.WithFile("yarn.lock", file)
	case Pnpm:
		m.ctr = m.ctr.WithExec([]string{"pnpm", "install", "--lockfile-only"})
	case Npm:
		m.ctr = m.ctr.
			WithExec([]string{"npm", "install", "--package-lock-only"})
	case BunManager:
		m.ctr = m.ctr.
			WithExec([]string{"bun", "install", "--no-verify", "--no-progress"})
	}

	return m
}

// Installs the dependencies using the detected package manager.
func (m *moduleRuntimeContainer) withInstalledDependencies() *moduleRuntimeContainer {
	switch m.cfg.packageManager {
	case Yarn:
		if semver.Compare(fmt.Sprintf("v%s", m.cfg.packageManagerVersion), "v3.0.0") <= 0 {
			m.ctr = m.ctr.
				WithExec([]string{"yarn", "install", "--frozen-lockfile"})
			break
		}

		m.ctr = m.ctr.WithExec([]string{"yarn", "install", "--immutable"})
	case Pnpm:
		m.ctr = m.ctr.
			WithExec([]string{"pnpm", "install", "--frozen-lockfile", "--shamefully-hoist=true"})
	case Npm:
		m.ctr = m.ctr.
			WithExec([]string{"npm", "ci"})
	case BunManager:
		m.ctr = m.ctr.
			WithExec([]string{"bun", "install", "--no-verify", "--no-progress"})
	case DenoManager:
		m.ctr = m.ctr.
			WithExec([]string{"deno", "install"})
	}

	return m
}

func (m *moduleRuntimeContainer) withUserSourceCode() *moduleRuntimeContainer {
	m.ctr = m.ctr.WithDirectory(
		ModSourceDirPath,
		m.cfg.contextDirectory,
		dagger.ContainerWithDirectoryOpts{
			// Include the rest of the user's module except config files to not override previous steps & SDKs.
			Exclude: append(moduleConfigFiles(m.cfg.subPath), filepath.Join(m.cfg.subPath, "sdk")),
		},
	)

	return m
}

// Returns the container with the generated SDK.
// If lib origin is bundled:
// - Add the bundle library (code.js & core.d.ts) to the sdk directory.
// - Add the static export setup (index.ts & client.gen.ts) to the sdk directory.
// - Generate the client.gen.ts file using the introspection file.
// If lib origin is local:
// - Copy the complete Typescript SDK directory
// - Generate the client.gen.ts file using the introspection file.
func (m *moduleRuntimeContainer) withGeneratedSDK(introspectionJSON *dagger.File) *moduleRuntimeContainer {
	var sdkDir *dagger.Directory

	switch m.cfg.sdkLibOrigin {
	case Bundle:
		sdkDir = m.sdkSourceDir.
			Directory("/bundled_lib").
			WithDirectory("/", bundledStaticDirectoryForModule()).
			WithFile("client.gen.ts", m.generateClient(introspectionJSON))
	case Local:
		sdkDir = m.sdkSourceDir.
			WithoutDirectory("codegen").
			WithoutDirectory("runtime").
			WithoutDirectory("tsx_module").
			WithoutDirectory("bundled_lib").
			WithoutDirectory("src/provisioning").
			WithFile("src/api/client.gen.ts", m.generateClient(introspectionJSON))
	case Remote:
		// TODO: Add support for remote SDK in module
		panic("remote sdk not supported yet in module")
	}

	m.ctr = m.ctr.
		WithDirectory(filepath.Join(m.cfg.modulePath(), GenDir), sdkDir)

	return m
}

// generateClient uses the given container to generate the client code.
func (m *moduleRuntimeContainer) generateClient(introspectionJSON *dagger.File) *dagger.File {
	codegenArgs := []string{
		codegenBinPath,
		"generate-module",
		"--lang", "typescript",
		"--output", ModSourceDirPath,
		"--module-name", m.cfg.name,
		"--module-source-path", m.cfg.modulePath(),
		"--introspection-json-path", schemaPath,
	}

	if m.cfg.sdkLibOrigin == Bundle {
		codegenArgs = append(codegenArgs, "--bundle")
	}

	return m.ctr.
		// Add dagger codegen binary.
		WithMountedFile(codegenBinPath, m.sdkSourceDir.File("/codegen")).
		// Mount the introspection file.
		WithMountedFile(schemaPath, introspectionJSON).
		// Execute the code generator using the given introspection file.
		WithExec(codegenArgs, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		// Return the generated code directory.
		Directory(m.cfg.sdkPath()).
		File("/src/api/client.gen.ts")
}

// Add the default template typescript file to the user's module.
// If the module has dependencies, generate passthrough functions.
func (m *moduleRuntimeContainer) withInitTemplate(ctx context.Context, introspectionJSON *dagger.File) *moduleRuntimeContainer {
	name := m.cfg.name

	// Parse introspection JSON to check for dependencies
	introspectionContent, err := introspectionJSON.Contents(ctx)
	if err != nil {
		// Fall back to default template on error
		m.ctr = m.ctr.WithDirectory(
			"src",
			templateDirectory().Directory("src"),
			dagger.ContainerWithDirectoryOpts{Include: []string{"*.ts"}},
		).
			WithExec([]string{"sed", "-i", "-e", fmt.Sprintf("s/QuickStart/%s/g", strcase.ToCamel(name)), "src/index.ts"})
		return m
	}

	var response introspection.Response
	if err := json.Unmarshal([]byte(introspectionContent), &response); err != nil {
		// Fall back to default template on error
		m.ctr = m.ctr.WithDirectory(
			"src",
			templateDirectory().Directory("src"),
			dagger.ContainerWithDirectoryOpts{Include: []string{"*.ts"}},
		).
			WithExec([]string{"sed", "-i", "-e", fmt.Sprintf("s/QuickStart/%s/g", strcase.ToCamel(name)), "src/index.ts"})
		return m
	}

	schema := response.Schema
	if schema == nil {
		// Fall back to default template if schema is nil
		m.ctr = m.ctr.WithDirectory(
			"src",
			templateDirectory().Directory("src"),
			dagger.ContainerWithDirectoryOpts{Include: []string{"*.ts"}},
		).
			WithExec([]string{"sed", "-i", "-e", fmt.Sprintf("s/QuickStart/%s/g", strcase.ToCamel(name)), "src/index.ts"})
		return m
	}

	// Extract dependencies
	dependencies := schema.ExtractDependencies()
	if len(dependencies) == 0 {
		// No dependencies, use default template
		m.ctr = m.ctr.WithDirectory(
			"src",
			templateDirectory().Directory("src"),
			dagger.ContainerWithDirectoryOpts{Include: []string{"*.ts"}},
		).
			WithExec([]string{"sed", "-i", "-e", fmt.Sprintf("s/QuickStart/%s/g", strcase.ToCamel(name)), "src/index.ts"})
		return m
	}

	// Generate passthrough module
	passthroughCode := generateTypeScriptPassthrough(name, dependencies)
	m.ctr = m.ctr.WithNewFile("src/index.ts", passthroughCode)

	return m
}

// Add the entrypoint to the container runtime so it can be called by the engine.
func (m *moduleRuntimeContainer) withEntrypoint() *moduleRuntimeContainer {
	m.ctr = m.ctr.WithMountedFile(
		m.cfg.entrypointPath(),
		entrypointFile(),
	)

	switch m.cfg.runtime {
	case Bun:
		m.ctr = m.ctr.
			WithEntrypoint([]string{"bun", m.cfg.entrypointPath()})
	case Deno:
		m.ctr = m.ctr.
			WithEntrypoint([]string{
				"deno", "run", "-A", m.cfg.entrypointPath(),
			})
	case Node:
		m.ctr = m.ctr.
			// need to specify --tsconfig because final runtime container will change working directory to a separate scratch
			// dir, without this the paths mapped in the tsconfig.json will not be used and js module loading will fail
			// need to specify --no-deprecation because the default package.json has no main field which triggers a warning
			// not useful to display to the user.
			WithEntrypoint([]string{"tsx", "--no-deprecation", "--tsconfig", m.cfg.tsConfigPath(), m.cfg.entrypointPath()})
	}

	return m
}

// generateTypeScriptPassthrough generates TypeScript code with passthrough functions for dependencies
func generateTypeScriptPassthrough(moduleName string, dependencies []*introspection.DependencyModule) string {
	var sb strings.Builder

	moduleClassName := strcase.ToCamel(moduleName)

	// Module header comment
	sb.WriteString(fmt.Sprintf(`/**
 * A generated module for %s functions
 *
 * This module has been generated from a blueprint and provides passthrough
 * functions to the original module.
 */
import { dag, object, func } from "@dagger.io/dagger"

@object()
export class %s {
`, moduleClassName, moduleClassName))

	// Generate passthrough functions for each dependency
	for _, dep := range dependencies {
		depAccessor := dep.Name // Keep camelCase for TypeScript

		for _, field := range dep.Functions {
			// Skip deprecated functions
			if field.IsDeprecated {
				continue
			}

			// Skip internal Id function
			if strings.ToLower(field.Name) == "id" {
				continue
			}

			funcName := field.Name // Keep camelCase for TypeScript

			// Build parameter list (only required args)
			var params []string
			var argsList []string

			for _, arg := range field.Args {
				// Skip optional arguments
				if arg.TypeRef.IsOptional() {
					continue
				}

				argName := arg.Name
				argType := formatTypeScriptType(arg.TypeRef)
				params = append(params, fmt.Sprintf("%s: %s", argName, argType))
				argsList = append(argsList, argName)
			}

			// Determine return type
			returnType := formatTypeScriptType(field.TypeRef)

			// Generate function with decorators
			sb.WriteString("\n  /**\n")
			if field.Description != "" {
				lines := strings.Split(strings.TrimSpace(field.Description), "\n")
				for _, line := range lines {
					sb.WriteString(fmt.Sprintf("   * %s\n", line))
				}
			} else {
				sb.WriteString(fmt.Sprintf("   * Calls the %s function from the %s dependency\n", field.Name, dep.Name))
			}
			sb.WriteString("   */\n")
			sb.WriteString("  @func()\n")

			// Generate function signature
			if len(params) > 0 {
				sb.WriteString(fmt.Sprintf("  %s(%s): %s {\n", funcName, strings.Join(params, ", "), returnType))
			} else {
				sb.WriteString(fmt.Sprintf("  %s(): %s {\n", funcName, returnType))
			}

			// Build the function call
			if len(argsList) > 0 {
				sb.WriteString(fmt.Sprintf("    return dag.%s().%s(%s)\n", depAccessor, funcName, strings.Join(argsList, ", ")))
			} else {
				sb.WriteString(fmt.Sprintf("    return dag.%s().%s()\n", depAccessor, funcName))
			}
			sb.WriteString("  }\n")
		}
	}

	sb.WriteString("}\n")
	return sb.String()
}

// formatTypeScriptType maps GraphQL types to TypeScript types
func formatTypeScriptType(typeRef *introspection.TypeRef) string {
	if typeRef == nil {
		return "void"
	}

	// Unwrap to get the base type
	unwrapped := typeRef.UnwrappedType()
	if unwrapped == nil {
		return "void"
	}

	var tsType string
	switch unwrapped.Name {
	case "String":
		tsType = "string"
	case "Int":
		tsType = "number"
	case "Float":
		tsType = "number"
	case "Boolean":
		tsType = "boolean"
	case "Void":
		tsType = "void"
	default:
		// For object types, use the type name as-is (they'll be imported from the SDK)
		tsType = unwrapped.Name
	}

	// Handle lists
	if typeRef.Kind == introspection.TypeKindList || (typeRef.OfType != nil && typeRef.OfType.Kind == introspection.TypeKindList) {
		// Check if it's a list by walking the type
		current := typeRef
		isList := false
		for current != nil {
			if current.Kind == introspection.TypeKindList {
				isList = true
				break
			}
			current = current.OfType
		}
		if isList {
			tsType = tsType + "[]"
		}
	}

	return tsType
}
