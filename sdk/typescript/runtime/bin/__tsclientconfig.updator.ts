import fs from "fs"

const daggerPathAlias = "@dagger.io/dagger"
const daggerTelemetryPathAlias = "@dagger.io/dagger/telemetry"
const daggerPath = "./sdk/src"
const daggerTelemetryPath = "./sdk/src/telemetry"
const daggerClientPathAlias = "@dagger.io/client"
const daggerClientPath = "./dagger/client.gen.ts"

const args = process.argv.slice(2)
let localSDK = false

switch (args.length) {
  case 0:
    break
  case 1:
    if (args[0] === "--local-sdk" || args[0] === "--local-sdk=true") {
      localSDK = true
      break
    }

    if (args[0] === "--local-sdk=false") {
      localSDK = false
      break
    }

    console.error(`Invalid flag configuration: ${args[0]}
Usage: ts_client_config_updator <local-sdk=true|false>`)
    process.exit(1)
    
    break
  default:
    console.error(`Usage: ts_client_config_updator <local-sdk=true|false>`)
    process.exit(1)
}

console.log(`Updating ts client configuration (localSDK=${localSDK})`)

const tsConfigPath = `./tsconfig.json`

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
      paths: {
        "@dagger.io/client": ["./dagger/client.gen.ts"],
      },
    },
  }

  if (localSDK) {
    defaultTsConfig.compilerOptions.paths[daggerPathAlias] = [daggerPath]
    defaultTsConfig.compilerOptions.paths[daggerTelemetryPathAlias] = [
      daggerTelemetryPath,
    ]
  }

  fs.writeFileSync(tsConfigPath, JSON.stringify(defaultTsConfig, null, 2))

  process.exit(0)
}

console.log(`Config file tsconfig.json exist. Updating it.`)

// Read the tsconfig.json file
const tsconfigFile = fs
  .readFileSync(tsConfigPath, "utf8")
  .split("\n")
  .reduce((acc: string[], line: string) => {
    if (line.startsWith("//") || (line.includes("/*") && line.includes("*/"))) {
      return acc
    }

    return [...acc, line]
  }, [])
  .join("\n")

// Remove comments and parse the tsconfig.json file
const tsconfig = JSON.parse(tsconfigFile)

// Add missing fields if there are
if (!tsconfig.compilerOptions) {
  tsconfig.compilerOptions = {}
}

if (!tsconfig.compilerOptions.paths) {
  tsconfig.compilerOptions.paths = {}
}

if (!tsconfig.compilerOptions.paths[daggerClientPathAlias]) {
  tsconfig.compilerOptions.paths[daggerClientPathAlias] = [daggerClientPath]
}

if (localSDK) {
  if (!tsconfig.compilerOptions.paths[daggerPathAlias]) {
    tsconfig.compilerOptions.paths[daggerPathAlias] = [daggerPath]
  }

  if (!tsconfig.compilerOptions.paths[daggerTelemetryPathAlias]) {
    tsconfig.compilerOptions.paths[daggerTelemetryPathAlias] = [
      daggerTelemetryPath,
    ]
  }
}

fs.writeFileSync(tsConfigPath, JSON.stringify(tsconfig, null, 2))

console.log(`tsconfig.json updated.`)
process.exit(0)
