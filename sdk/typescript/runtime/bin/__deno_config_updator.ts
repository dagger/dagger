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
 *   deno_config_updator --sdk-lib-origin=bundle|local|remote --standalone-client=true|false --output-dir=string --default-typescript-version=string
 *
 * Note: The file is heavily documented because it's a single script and it can be quite
 * confusing when reading through it otherwise.
 */

/*******************************************************************************
 * CLI configuration and parsing
 ******************************************************************************/
const help = `Usage: deno_config_updator <--sdk-lib-origin=bundle|local> --standalone-client=true|false --output-dir=string> --default-typescript-version=string>`
const args = Deno.args

class Arg<T> {
  constructor(
    public name: string,
    public value: T | null,
  ) {}
}

const sdkLibOrigin = new Arg<string>("sdk-lib-origin", null)
const standaloneClient = new Arg<boolean>("standalone-client", false)
const clientDir = new Arg<string>("client-dir", null)
const defaultTypeScriptVersion = new Arg<string>(
  "default-typescript-version",
  null,
)

// Parse arguments from the CLI.
for (const arg of args) {
  const [name, value] = arg.slice("--".length).split("=")
  switch (name) {
    case sdkLibOrigin.name:
      if (
        !value ||
        (value !== "bundle" && value !== "local" && value != "remote")
      ) {
        console.error(
          `Invalid value for ${sdkLibOrigin.name}: ${value}.\n${help}`,
        )
        Deno.exit(1)
      }

      sdkLibOrigin.value = value
      break
    case standaloneClient.name:
      if (value === undefined || value === "true") {
        standaloneClient.value = true
        break
      }

      if (value === "false") {
        standaloneClient.value = false
        break
      }

      console.error(
        `Invalid value for ${standaloneClient.name}: ${value}\n ${help}`,
      )
      Deno.exit(1)

      break
    case clientDir.name:
      if (value === undefined) {
        console.error(`Missing value for ${clientDir.name}\n ${help}`)
        Deno.exit(1)
      }

      clientDir.value = value

      break
    case defaultTypeScriptVersion.name:
      if (value === undefined) {
        console.error(
          `Missing value for ${defaultTypeScriptVersion.name}\n ${help}`,
        )
        Deno.exit(1)
      }

      defaultTypeScriptVersion.value = value

      break
  }
}

if (!sdkLibOrigin.value) {
  console.error(`Missing value for ${sdkLibOrigin.name}.\n${help}`)
  Deno.exit(1)
}

if (standaloneClient.value === true && clientDir.value === null) {
  console.error(
    `Missing output-dir argument while standalone client is set to true\n${help}`,
  )
  Deno.exit(1)
}

if (defaultTypeScriptVersion.value === null) {
  console.error(`Missing default-typescript-version argument\n${help}`)
  Deno.exit(1)
}

/*******************************************************************************
 * Constants config section
 ******************************************************************************/
const denoConfigPath = "./deno.json"

// Import paths used by user.
const daggerPathAlias = "@dagger.io/dagger"
const daggerTelemetryPathAlias = "@dagger.io/dagger/telemetry"
const daggerClientPathAlias = "@dagger.io/client"

// Filename of imported path aliases.
const daggerRootFilename = {
  bundle: "./sdk/index.ts",
  local: "./sdk/src/index.ts",
}

const daggerTelemetryFilename = {
  bundle: "./sdk/telemetry.ts",
  local: "./sdk/src/telemetry/index.ts",
}

const typescriptImport = `npm:typescript@${defaultTypeScriptVersion.value}`

// Imports map to be added to the deno.json file.
const daggerImports = {
  [daggerPathAlias]: `${daggerRootFilename[sdkLibOrigin.value!]}`,
  [daggerTelemetryPathAlias]: `${daggerTelemetryFilename[sdkLibOrigin.value!]}`,
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

console.log(`
  Updating deno.json (sdkLibOrigin=${sdkLibOrigin.value}, standaloneClient=${standaloneClient.value}, clientDir=${clientDir.value})
`)

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

if (denoConfig.imports["typescript"] === undefined) {
  denoConfig.imports["typescript"] = typescriptImport
}

if (sdkLibOrigin.value !== "remote") {
  for (const [key, value] of Object.entries(daggerImports)) {
    denoConfig.imports[key] = value
  }
}

if (standaloneClient.value === true) {
  denoConfig.compilerOptions.paths[daggerClientPathAlias] = [
    `./${clientDir.value}/client.gen.ts`,
  ]
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

if (standaloneClient.value === false) {
  denoConfig.compilerOptions.experimentalDecorators = true
}

// Update nodeModulesDir
if (!denoConfig.nodeModulesDir) {
  denoConfig.nodeModulesDir = "auto"
}
denoConfig.nodeModulesDir = "auto"

console.log(`deno.json updated.`)

// Write file back
Deno.writeTextFileSync(denoConfigPath, JSON.stringify(denoConfig, null, 2))
Deno.exit(0)
