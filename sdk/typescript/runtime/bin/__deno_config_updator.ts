const denoConfigPath = "./deno.json"
const daggerImports = {
  "@dagger.io/dagger": "./sdk/index.ts",
  "@dagger.io/dagger/telemetry": "./sdk/telemetry.ts",
  typescript: "npm:typescript@^5.8.2",
}
const unstableFlags = [
  "bare-node-builtins",
  "sloppy-imports",
  "node-globals",
  "byonm",
]

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
