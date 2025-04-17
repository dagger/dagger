package main

import (
	"fmt"
	"path/filepath"
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsdistconsts"

	"github.com/iancoleman/strcase"
	"golang.org/x/mod/semver"
)

// Base returns a Node, Bun or Deno base container with utilities and cache
// setup.
func (t *TypescriptSdk) runtimeBaseContainer() *dagger.Container {
	ctr := dag.Container().From(t.moduleConfig.image)

	runtime := t.moduleConfig.runtime
	version := t.moduleConfig.runtimeVersion

	switch runtime {
	case Bun:
		return ctr.
			WithoutEntrypoint().
			WithMountedCache("/root/.bun/install/cache", dag.CacheVolume(fmt.Sprintf("mod-bun-cache-%s", tsdistconsts.DefaultBunVersion)), dagger.ContainerWithMountedCacheOpts{
				Sharing: dagger.CacheSharingModePrivate,
			})
	case Deno:
		return ctr.
			WithoutEntrypoint().
			WithMountedCache("/root/.deno/cache", dag.CacheVolume(fmt.Sprintf("mod-deno-cache-%s", tsdistconsts.DefaultDenoVersion)))
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
			WithExec([]string{"ln", "-s", "/usr/local/lib/node_modules/tsx/dist/cli.mjs", "/usr/local/bin/tsx"})
	default:
		// This cannot happen since runtime always default to Node or error
		// during analyseModuleConfig
		return ctr
	}
}

// Configure the user's module based on the detected runtime.
//
// For Bun and Node:
// - If no `package.json` is present, it will create a default one with a `tsconfig.json`
// using the template directory .
// - If `package.json` is present, it will update the `package.json` with
// correct setup and update the `tsconfig.json` using the `bin/__tsconfig.updator.ts` script.
//
// For Deno:
// - It will update the `deno.json` with correct setup using the `bin/__deno_config_updator.ts` script.
func (t *TypescriptSdk) withConfiguredRuntimeEnvironment(ctr *dagger.Container) *dagger.Container {
	runtime := t.moduleConfig.runtime

	ctr = ctr.WithDirectory(
		ModSourceDirPath,
		t.moduleConfig.contextDirectory,
		dagger.ContainerWithDirectoryOpts{
			Include: t.moduleConfigFiles(t.moduleConfig.subPath),
		},
	).
		WithWorkdir(t.moduleConfig.modulePath())

	switch runtime {
	case Bun:
		if t.moduleConfig.packageJSONConfig == nil {
			return ctr.
				WithDirectory(".",
					templateDirectory(),
					dagger.ContainerWithDirectoryOpts{Include: []string{"*.json"}},
				)
		}

		return ctr.
			WithMountedFile("/opt/module/bin/__tsconfig.updator.ts", tsConfigUpdatorFile()).
			WithExec([]string{"bun", "/opt/module/bin/__tsconfig.updator.ts", fmt.Sprintf("--sdk-lib-origin=%s", t.moduleConfig.sdkLibOrigin)}).
			WithFile("package.json", t.configurePackageJSON(ctr.File("package.json")))
	case Node:
		if t.moduleConfig.packageJSONConfig == nil {
			return ctr.
				WithDirectory(".",
					templateDirectory(),
					dagger.ContainerWithDirectoryOpts{Include: []string{"*.json"}},
				)
		}

		return ctr.
			WithMountedFile("/opt/module/bin/__tsconfig.updator.ts", tsConfigUpdatorFile()).
			WithExec([]string{"tsx", "/opt/module/bin/__tsconfig.updator.ts", fmt.Sprintf("--sdk-lib-origin=%s", t.moduleConfig.sdkLibOrigin)}).
			WithFile("package.json", t.configurePackageJSON(ctr.File("package.json")))
	case Deno:
		return ctr.
			WithMountedFile("/opt/module/bin/__deno_config_updator.ts", denoConfigUpdatorFile()).
			WithExec([]string{"deno", "run", "-A", "/opt/module/bin/__deno_config_updator.ts", fmt.Sprintf("--sdk-lib-origin=%s", t.moduleConfig.sdkLibOrigin)})
	default:
		// This should never happens, but if it does case we simply return the container
		// as it is.
		return ctr
	}
}

// Update the user's package.json with required dependencies to use the
// Typescript SDK.
// We need to use a node container to run npm since it's not available on
// bun (https://github.com/oven-sh/bun/issues/9840).
// This fucntion can be removed once supported by bun so we
// can gain some performance.
func (t *TypescriptSdk) configurePackageJSON(file *dagger.File) *dagger.File {
	ctr := dag.
		Container().
		From(tsdistconsts.DefaultNodeImageRef).
		WithDirectory("/src", dag.Directory().WithFile("package.json", file)).
		WithWorkdir("/src").
		WithExec([]string{"npm", "pkg", "set", "type=module"})

	if t.moduleConfig.packageJSONConfig != nil {
		_, ok := t.moduleConfig.packageJSONConfig.Dependencies["typescript"]
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
func (t *TypescriptSdk) withSetupPackageManager(ctr *dagger.Container) *dagger.Container {
	packageManager := t.moduleConfig.packageManager
	version := t.moduleConfig.packageManagerVersion

	switch packageManager {
	case Yarn:
		return ctr.
			WithExec([]string{"corepack", "enable"}).
			WithExec([]string{"corepack", "use", fmt.Sprintf("yarn@%s", version)})
	case Pnpm:
		ctr = ctr.WithExec([]string{"npm", "install", "-g", fmt.Sprintf("pnpm@%s", version)})

		if !t.moduleConfig.hasFile("pnpm-workspace.yaml") && t.moduleConfig.sdkLibOrigin == Local {
			ctr = ctr.
				WithNewFile("pnpm-workspace.yaml", `packages:
  - './sdk'
			`)
		}

		return ctr
	case Npm:
		return ctr.
			WithExec([]string{"npm", "install", "-g", fmt.Sprintf("npm@%s", version)})
	case BunManager, DenoManager:
		return ctr
	default:
		// This should never happens, but if it does case we simply return the container
		// as it is.
		return ctr
	}
}

// Generate a lock file for the matching package manager.
func (t *TypescriptSdk) withGeneratedLockFile(ctr *dagger.Container) *dagger.Container {
	packageManager := t.moduleConfig.packageManager

	switch packageManager {
	case Yarn:
		// Install dependencies and extract the lockfile
		file := ctr.
			WithExec([]string{"yarn", "install", "--mode", "update-lockfile"}).File("yarn.lock")

		// We use node-modules linker for yarn >= v3 because it's not working with pnp.
		if semver.Compare(fmt.Sprintf("v%s", t.moduleConfig.packageManagerVersion), "v3.0.0") >= 0 {
			ctr = ctr.WithNewFile(".yarnrc.yml", `nodeLinker: node-modules`)
		}

		// Sadly, yarn < v3 doesn't support generating a lockfile without installing the dependencies.
		// So we use npm to generate the lockfile and then import it into yarn.
		return ctr.WithFile("yarn.lock", file)
	case Pnpm:
		return ctr.WithExec([]string{"pnpm", "install", "--lockfile-only"})
	case Npm:
		return ctr.
			WithExec([]string{"npm", "install", "--package-lock-only"})
	case BunManager:
		return ctr.
			WithExec([]string{"bun", "install", "--no-verify", "--no-progress"})
	case DenoManager:
		return ctr
	default:
		// This should never happens, but if it does case we simply return the container
		// as it is.
		return ctr
	}
}

// Installs the dependencies using the detected package manager.
func (t *TypescriptSdk) withInstalledDependencies(ctr *dagger.Container) *dagger.Container {
	switch t.moduleConfig.packageManager {
	case Yarn:
		if semver.Compare(fmt.Sprintf("v%s", t.moduleConfig.packageManagerVersion), "v3.0.0") <= 0 {
			return ctr.
				WithExec([]string{"yarn", "install", "--frozen-lockfile"})
		}

		return ctr.WithExec([]string{"yarn", "install", "--immutable"})
	case Pnpm:
		return ctr.
			WithExec([]string{"pnpm", "install", "--frozen-lockfile", "--shamefully-hoist=true"})
	case Npm:
		return ctr.
			WithExec([]string{"npm", "ci"})
	case BunManager:
		return ctr.
			WithExec([]string{"bun", "install", "--no-verify", "--no-progress"})
	case DenoManager:
		return ctr.
			WithExec([]string{"deno", "install"})
	default:
		// This should never happens, but if it does case we simply return the container
		// as it is.
		return ctr
	}
}

func (t *TypescriptSdk) withUserSourceCode(ctr *dagger.Container) *dagger.Container {
	return ctr.WithDirectory(
		ModSourceDirPath,
		t.moduleConfig.contextDirectory,
		dagger.ContainerWithDirectoryOpts{
			// Include the rest of the user's module except config files to not override previous steps & SDKs.
			Exclude: append(t.moduleConfigFiles(t.moduleConfig.subPath), filepath.Join(t.moduleConfig.subPath, "sdk")),
		},
	)
}

// Returns the container with the generated SDK.
// If lib origin is bundled:
// - Add the bundle library (code.js & core.d.ts) to the sdk directory.
// - Add the static export setup (index.ts & client.gen.ts) to the sdk directory.
// - Generate the client.gen.ts file using the introspection file.
// If lib origin is local:
// - Copy the complete Typescript SDK directory
// - Generate the client.gen.ts file using the introspection file.
func (t *TypescriptSdk) withGeneratedSDK(introspectionJSON *dagger.File) func(ctr *dagger.Container) *dagger.Container {
	return func(ctr *dagger.Container) *dagger.Container {
		var sdkDir *dagger.Directory

		switch t.moduleConfig.sdkLibOrigin {
		case Bundle:
			sdkDir = t.SDKSourceDir.
				Directory("/bundled_lib").
				WithDirectory("/", bundledStaticDirectoryForModule()).
				WithFile("client.gen.ts", t.generateClient(ctr, introspectionJSON))
		case Local:
			sdkDir = t.SDKSourceDir.
				WithoutDirectory("codegen").
				WithoutDirectory("runtime").
				WithoutDirectory("tsx_module").
				WithoutDirectory("bundled_Lib").
				WithoutDirectory("src/provisioning").
				WithFile("src/api/client.gen.ts", t.generateClient(ctr, introspectionJSON))
		default:
			// This should never happens since detectSdkLibOrigin default to Bundle.
			sdkDir = dag.Directory()
		}

		return ctr.
			WithDirectory(filepath.Join(t.moduleConfig.modulePath(), GenDir), sdkDir)
	}
}

// generateClient uses the given container to generate the client code.
func (t *TypescriptSdk) generateClient(ctr *dagger.Container, introspectionJSON *dagger.File) *dagger.File {
	codegenArgs := []string{
		codegenBinPath,
		"--lang", "typescript",
		"--output", ModSourceDirPath,
		"--module-name", t.moduleConfig.name,
		"--module-source-path", t.moduleConfig.modulePath(),
		"--introspection-json-path", schemaPath,
	}

	if t.moduleConfig.sdkLibOrigin == Bundle {
		codegenArgs = append(codegenArgs, "--bundle")
	}

	return ctr.
		// Add dagger codegen binary.
		WithMountedFile(codegenBinPath, t.SDKSourceDir.File("/codegen")).
		// Mount the introspection file.
		WithMountedFile(schemaPath, introspectionJSON).
		// Execute the code generator using the given introspection file.
		WithExec(codegenArgs, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		// Return the generated code directory.
		Directory(t.moduleConfig.sdkPath()).
		File("/src/api/client.gen.ts")
}

// Add the default template typescript file to the user's module
func (t *TypescriptSdk) withInitTemplate(ctr *dagger.Container) *dagger.Container {
	name := t.moduleConfig.name

	return ctr.WithDirectory(
		"src",
		templateDirectory().Directory("src"),
		dagger.ContainerWithDirectoryOpts{Include: []string{"*.ts"}},
	).
		WithExec([]string{"sed", "-i", "-e", fmt.Sprintf("s/QuickStart/%s/g", strcase.ToCamel(name)), "src/index.ts"})
}
