import * as fs from "fs"

const tsConfigPath = `./tsconfig.json`

const daggerPathAlias = "@dagger.io/dagger"
const daggerTelemetryPathAlias = "@dagger.io/dagger/telemetry"
const daggerPath = "./sdk"
const daggerTelemetryPath = "./sdk/telemetry"

// If the tsconfig.json file doesn't exist, create it with default config.
if (!fs.existsSync(tsConfigPath)) {
  const defaultTsConfig = {
    compilerOptions: {
      target: "ES2022",
      moduleResolution: "Node",
      experimentalDecorators: true,
      paths: {
        "@dagger.io/dagger": ["./sdk"],
        "@dagger.io/dagger/telemetry": ["./sdk/telemetry"],
      },
    },
  }

  fs.writeFileSync(tsConfigPath, JSON.stringify(defaultTsConfig, null, 2))

  process.exit(0)
}

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

// If `@dagger.io/dagger` isn't part of the tsconfig paths, update it.
if (
  !tsconfig.compilerOptions.paths[daggerPathAlias] ||
  !tsconfig.compilerOptions.paths[daggerPathAlias].includes(daggerPath)
) {
  tsconfig.compilerOptions.paths[daggerPathAlias] = [daggerPath]
  tsconfig.compilerOptions.paths[daggerTelemetryPathAlias] = [
    daggerTelemetryPath,
  ]

  fs.writeFileSync(tsConfigPath, JSON.stringify(tsconfig, null, 2))
}
