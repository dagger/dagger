/**
 * DenoConfigUpdate is a utility script to configure the user `deno.json` file.
 *
 * The config needs few things to be usable with dagger:
 * - `unstableFlags` to enable experimental features that make the TypeScript SDK compatible with Deno (since it's built on Node)
 *   * bare-node-builtins: for compatibility with the Node native ecosystem (path, fs, module etc...)
 *   * sloppy-imports: for compatibility with the Node import system with `.js` extensions.
 *   * node-globals: for compatibility with the Node global variables (process, global, Buffer etc...)
 *   * byonm: for compatibility with `node_modules` dependencies.
 * - `paths` to be set to the correctl location of the SDK library
 * - `experimentalDecorators`: to be `true` for `@func`, `@object`, `@arguments`...
 * - `nodeModulesDir`: to be `true` so it installs dependencies locally inside
 * `node_modules` instead of relying on symlink that may break in the TS runtime
 *
 * Depending on the target location of the SDK library, either paths will be set to:
 * - `./sdk` for the bundle SDK.
 * - `./sdk/src` for the local SDK.
 *
 * If any value is already set with a wrong value, the script will update it to its expected value.
 *
 * Usage:
 *   deno_config_updator --sdk-lib-origin=bundle|local
 *
 * Note: The file is heavily documented because it's a single script and it can be quite
 * confusing when reading through it otherwise.
 */

/*******************************************************************************
 * CLI configuration and parsing
 ******************************************************************************/
const help = `Usage: deno_config_updator <--sdk-lib-origin=bundle|local>`
const args = Deno.args

class Arg<T> {
  constructor(
    public name: string,
    public value: T | null,
  ) {}
}

const sdkLibOrigin = new Arg<string>("sdk-lib-origin", null)

// Parse arguments from the CLI.
for (const arg of args) {
  const [name, value] = arg.slice("--".length).split("=")
  switch (name) {
    case sdkLibOrigin.name:
      if (!value || (value !== "bundle" && value !== "local")) {
        console.error(
          `Invalid value for ${sdkLibOrigin.name}: ${value}.\n${help}`,
        )
        Deno.exit(1)
      }

      sdkLibOrigin.value = value
      break
  }
}

if (!sdkLibOrigin.value) {
  console.error(`Missing value for ${sdkLibOrigin.name}.\n${help}`)
  Deno.exit(1)
}

/*******************************************************************************
 * Constants config section
 ******************************************************************************/
const denoConfigPath = "./deno.json"

// Filename of imported path aliases.
const daggerRootFilename = {
  bundle: "./sdk/index.ts",
  local: "./sdk/src/index.ts",
}

const daggerTelemetryFilename = {
  bundle: "./sdk/telemetry.ts",
  local: "./sdk/src/telemetry/index.ts",
}

// Imports map to be added to the deno.json file.
const daggerImports = {
  "@dagger.io/dagger": `${daggerRootFilename[sdkLibOrigin.value!]}`,
  "@dagger.io/dagger/telemetry": `${daggerTelemetryFilename[sdkLibOrigin.value!]}`,
  typescript: "npm:typescript@^5.8.2",
}

// Unstable flags to set in the deno.json file.
const unstableFlags = [
  "bare-node-builtins",
  "sloppy-imports",
  "node-globals",
  "byonm",
]

/*******************************************************************************
 * Main script
 ******************************************************************************/

// Read the deno.json file
const denoConfigFile = Deno.readTextFileSync(denoConfigPath)
  // Cleanup comments
  .split("\n")
  .reduce((acc: string[], line: string) => {
    if (line.includes("//") || (line.includes("/*") && line.includes("*/"))) {
      return acc
    }

    return [...acc, line]
  }, [])
  .join("\n")

const denoConfig = JSON.parse(denoConfigFile)

// Update imports statements
if (!denoConfig.imports) {
  denoConfig.imports = {}
}

for (const [key, value] of Object.entries(daggerImports)) {
  if (!denoConfig.imports[key]) {
    denoConfig.imports[key] = value
  }
}

// Update unstable features
if (!denoConfig.unstable) {
  denoConfig.unstable = []
}

for (const flag of unstableFlags) {
  if (!denoConfig.unstable.includes(flag)) {
    denoConfig.unstable.push(flag)
  }
}

// Update compiler options
if (!denoConfig.compilerOptions) {
  denoConfig.compilerOptions = {}
}

denoConfig.compilerOptions.experimentalDecorators = true

// Update nodeModulesDir
if (!denoConfig.nodeModulesDir) {
  denoConfig.nodeModulesDir = "auto"
}
denoConfig.nodeModulesDir = "auto"

// Write file back
Deno.writeTextFileSync(denoConfigPath, JSON.stringify(denoConfig, null, 2))
Deno.exit(0)
