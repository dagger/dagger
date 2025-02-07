import fs from "fs"
import path from "path"

const daggerPathAlias = "@dagger.io/dagger"
const daggerTelemetryPathAlias = "@dagger.io/dagger/telemetry"
const daggerPath = "./sdk/src"
const daggerTelemetryPath = "./sdk/src/telemetry"
const daggerClientPathAlias = "@dagger.io/client"

const help = `Usage: ts_client_config_updator <local-sdk=true|false> <output-dir=string>`
const args = process.argv.slice(2)

class Arg<T> {
  constructor(
    public name: string,
    public value: T | null,
  ) {}
}

const localSDK = new Arg<boolean>("local-sdk", false)
const libraryDir = new Arg<string>("library-dir", null)

for (const arg of args) {
  const [name, value] = arg.slice("--".length).split("=")
  switch (name) {
    case "local-sdk":
      if (value === undefined || value === "true") {
        localSDK.value = true
        break
      }

      if (value === "false") {
        localSDK.value = false
        break
      }

      console.error(`Invalid value for local-sdk: ${value}\n ${help}`)
      process.exit(1)

      break
    case "library-dir":
      libraryDir.value = value
      break
  }
}

if (libraryDir.value === null) {
  console.error(`Missing library-dir argument\n${help}`)
  process.exit(1)
}

console.log(
  `Updating ts client configuration (localSDK=${localSDK.value}) (libraryDir=${libraryDir.value})`,
)

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
        "@dagger.io/client": [`./${libraryDir.value}/client.gen.ts`],
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

tsconfig.compilerOptions.paths[daggerClientPathAlias] = [
  path.join(libraryDir.value, "client.gen.ts"),
]

if (localSDK) {
  tsconfig.compilerOptions.paths[daggerPathAlias] = [daggerPath]
  tsconfig.compilerOptions.paths[daggerTelemetryPathAlias] = [
    daggerTelemetryPath,
  ]
}

fs.writeFileSync(tsConfigPath, JSON.stringify(tsconfig, null, 2))

console.log(`tsconfig.json updated.`)
process.exit(0)
