import * as fs from "fs"
import * as path from "path"
import { fileURLToPath } from "url"

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

const moduleProjectDirectory = `${__dirname}/../../`
const tsConfigPath = `${moduleProjectDirectory}/tsconfig.json`

const daggerPathAlias = "@dagger.io/dagger"
const daggerPath = "./sdk"

// If the tsconfig.json file doesn't exist, create it with default config.
if (!fs.existsSync(tsConfigPath)) {
  const defaultTsConfig = {
    compilerOptions: {
      target: "ES2022",
      moduleResolution: "Node",
      experimentalDecorators: true,
      paths: {
        "@dagger.io/dagger": ["./sdk"],
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

  fs.writeFileSync(tsConfigPath, JSON.stringify(tsconfig, null, 2))
}
