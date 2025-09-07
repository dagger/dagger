/**
 * TsConfigUpdator is a utility script to configure the user `tsconfig.json` file inside
 * his module.
 * If no tsconfig.json file is found, it will create a default one.
 *
 * The config needs few things to be usable with dagger:
 * - `target` to be `ES2022`
 * - `moduleResolution` to be `Node`
 * - `experimentalDecorators` to be `true` for `@func`, `@object`, `@arguments`...
 * - `paths` to be set to the correct location of the SDK library
 *
 * Depending on the target location of the SDK library, either paths will be set to:
 * - `./sdk` for the bundle SDK.
 * - `./sdk/src` for the local SDK.
 * - nothing in case of the remote SDK.
 *
 * If any value is already set but wrong, the script will update it to its expected value.
 *
 * If standalone client is set to true, the script expects output-dir to be set; otherwise, it's optional.
 *
 * Usage:
 *   ts_config_updator --sdk-lib-origin=bundle|local|remote --standalone-client=true|false --output-dir=string
 *
 * Note: The file is heavily documented because it's a single script and it can be quite
 * confusing when reading through it otherwise.
 */
import * as fs from "fs"

/*******************************************************************************
 * CLI configuration and parsing
 ******************************************************************************/
const help = `Usage: ts_config_updator <--sdk-lib-origin=bundle|local|remote> <--standalone-client=true|false> <--output-dir=string>`
const args = process.argv.slice(2)

class Arg<T> {
  constructor(
    public name: string,
    public value: T | null,
  ) {}
}

const sdkLibOrigin = new Arg<string>("sdk-lib-origin", null)
const standaloneClient = new Arg<boolean>("standalone-client", false)
const clientDir = new Arg<string>("client-dir", null)

// Parse arguments from the CLI
for (const arg of args) {
  const [name, value] = arg.slice("--".length).split("=")
  switch (name) {
    case "sdk-lib-origin":
      if (value === undefined) {
        console.error(`Missing value for ${name}\n ${help}`)
        process.exit(1)
      }

      if (value !== "bundle" && value !== "local" && value != "remote") {
        console.error(`Invalid value for ${name}: ${value}\n ${help}`)
        process.exit(1)
      }

      sdkLibOrigin.value = value

      break
    case "standalone-client":
      if (value === undefined || value === "true") {
        standaloneClient.value = true
        break
      }

      if (value === "false") {
        standaloneClient.value = false
        break
      }

      console.error(`Invalid value for ${name}: ${value}\n ${help}`)
      process.exit(1)

      break
    case "client-dir":
      if (value === undefined) {
        console.error(`Missing value for ${name}\n ${help}`)
        process.exit(1)
      }

      clientDir.value = value

      break
  }
}

if (sdkLibOrigin.value === null) {
  console.error(`Missing sdk-lib-origin argument\n${help}`)
  process.exit(1)
}

if (standaloneClient.value === true && clientDir.value === null) {
  console.error(
    `Missing output-dir argument while standalone client is set to true\n${help}`,
  )
  process.exit(1)
}

/*******************************************************************************
 * Constants config section
 ******************************************************************************/
const tsConfigPath = `./tsconfig.json`

// Import paths used by user.
const daggerPathAlias = "@dagger.io/dagger"
const daggerTelemetryPathAlias = "@dagger.io/dagger/telemetry"
const daggerClientPathAlias = "@dagger.io/client"

// Filename of imported path aliases.
const daggerRootFilename = {
  bundle: "./sdk/index.ts",
  local: "./sdk/src",
  // no value for remote, since it's handled by the package manager.
}
const daggerTelemetryFilename = {
  bundle: "./sdk/telemetry.ts",
  local: "./sdk/src/telemetry",
  // no value for remote, since it's handled by the package manager.
}

/*******************************************************************************
 * Main script
 ******************************************************************************/

console.log(`
  Updating TSConfig (sdkLibOrigin=${sdkLibOrigin.value}, standaloneClient=${standaloneClient.value}, clientDir=${clientDir.value})
`)

// If the tsconfig.json file doesn't exist, create it with default config.
if (!fs.existsSync(tsConfigPath)) {
  console.log(
    `Config file tsconfig.json doesn't exist. Creating default tsconfig.json.`,
  )

  const defaultTsConfig = {
    compilerOptions: {
      target: "ES2022",
      moduleResolution: "Node",
      experimentalDecorators: true,
      strict: true,
      skipLibCheck: true,
      paths: {},
    },
  }

  if (sdkLibOrigin.value !== "remote") {
    defaultTsConfig.compilerOptions.paths = {
      [daggerPathAlias]: [`${daggerRootFilename[sdkLibOrigin.value!]}`],
      [daggerTelemetryPathAlias]: [
        `${daggerTelemetryFilename[sdkLibOrigin.value!]}`,
      ],
    }
  }

  if (standaloneClient.value === true) {
    defaultTsConfig.compilerOptions.paths[daggerClientPathAlias] = [
      `./${clientDir.value}/client.gen.ts`,
    ]
  }

  fs.writeFileSync(tsConfigPath, JSON.stringify(defaultTsConfig, null, 2))

  process.exit(0)
}

console.log(`Config file tsconfig.json exists. Updating it.`)

// Read the tsconfig.json file
const tsconfigFile = fs
  .readFileSync(tsConfigPath, "utf8")
  .split("\n")
  .reduce((acc: string[], line: string) => {
    // We need to remove comments here because JSON.parse fails otherwise.
    if (line.startsWith("//") || (line.includes("/*") && line.includes("*/"))) {
      return acc
    }

    return [...acc, line]
  }, [])
  .join("\n")

// Parse the tsconfig.json file into a JSON struct
const tsconfig = JSON.parse(tsconfigFile)

// Add missing fields if there are
if (!tsconfig.compilerOptions) {
  tsconfig.compilerOptions = {}
}

// Set experimentalDecorators to true
if (standaloneClient.value === false) {
  tsconfig.compilerOptions.experimentalDecorators = true
}

// Update paths in the TSConfig file
if (!tsconfig.compilerOptions.paths) {
  tsconfig.compilerOptions.paths = {}
}

if (sdkLibOrigin.value !== "remote") {
  tsconfig.compilerOptions.paths[daggerPathAlias] = [
    `${daggerRootFilename[sdkLibOrigin.value!]}`,
  ]
  tsconfig.compilerOptions.paths[daggerTelemetryPathAlias] = [
    `${daggerTelemetryFilename[sdkLibOrigin.value!]}`,
  ]
}

if (standaloneClient.value === true) {
  tsconfig.compilerOptions.paths[daggerClientPathAlias] = [
    `./${clientDir.value}/client.gen.ts`,
  ]
}

console.log(`tsconfig.json updated.`)

// Write the updated TSConfig file
fs.writeFileSync(tsConfigPath, JSON.stringify(tsconfig, null, 2))
process.exit(0)
