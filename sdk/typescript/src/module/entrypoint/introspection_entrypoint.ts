import * as fs from "fs"
import * as path from "path"

import { connection } from "../../connect.js"
import { scan } from "../introspector/index.js"
import { serializeModule } from "../introspector/typedef_json.js"
import { Register } from "./register.js"

async function introspection(
  files: string[],
  moduleName: string,
  generatedClientFiles: string[],
) {
  return await scan(files, moduleName, false, generatedClientFiles)
}

const allowedExtensions = [".ts", ".mts"]

function getTsSourceCodeFiles(dir: string): string[] {
  return fs
    .readdirSync(dir)
    .map((file) => {
      const filepath = path.join(dir, file)

      const stat = fs.statSync(filepath)

      if (stat.isDirectory()) {
        return getTsSourceCodeFiles(filepath)
      }

      const ext = path.extname(filepath)
      if (allowedExtensions.find((allowedExt) => allowedExt === ext)) {
        return [path.join(dir, file)]
      }

      return []
    })
    .reduce((p, c) => [...c, ...p], [])
}

// generatedClientFiles returns every `*.gen.ts` file sitting next to the given
// client file (client.gen.ts and each per-dependency <dep>.gen.ts). Falls back
// to just the client file if the directory can't be listed.
function generatedClientFiles(clientFile: string): string[] {
  const dir = path.dirname(clientFile)
  try {
    const files = fs
      .readdirSync(dir)
      .filter((f) => f.endsWith(".gen.ts"))
      .map((f) => path.join(dir, f))
    return files.length > 0 ? files : [clientFile]
  } catch {
    return [clientFile]
  }
}

async function main() {
  const args = process.argv.slice(2)
  if (args.length < 3) {
    console.log(
      "usage: introspection <moduleName> <userSourceCodeDir> <typescriptClientFile>",
    )
    process.exit(1)
  }

  const moduleName = args[0]
  const userSourceCodeDir = args[1]
  const typescriptClientFile = args[2]

  const userSourceCodeFiles = getTsSourceCodeFiles(userSourceCodeDir)

  // The generated client is split across `client.gen.ts` and one
  // `<dep>.gen.ts` per dependency. Pass every `*.gen.ts` sibling so the
  // introspector can resolve types contributed by dependencies (e.g. an enum
  // re-exported from a dep file), not just those declared in client.gen.ts.
  const clientGenFiles = generatedClientFiles(typescriptClientFile)

  const result = await introspection(
    [...userSourceCodeFiles, ...clientGenFiles],
    moduleName,
    clientGenFiles,
  )

  if (process.env.DRY_RUN) {
    console.log(JSON.stringify(result, null, 2))
    process.exit(0)
  }

  // When EMIT_TYPEDEF_JSON_FILE is set, write the parsed DaggerModule as a
  // stable JSON typedef. The Go-side `cmd/codegen generate-entrypoint`
  // subcommand consumes that JSON to render the static dispatch
  // __dagger.entrypoint.ts file.
  const typedefJsonPath = process.env.EMIT_TYPEDEF_JSON_FILE
  if (typedefJsonPath) {
    const json = serializeModule(result)
    await fs.promises.writeFile(typedefJsonPath, JSON.stringify(json))
    if (!process.env.TYPEDEF_OUTPUT_FILE) {
      return
    }
  }

  // TODO(TomChv): move that logic inside the engine at some point
  // so we don't even need a connection.
  // Idea: We should output a JSON schema of the module that can be transformed
  // into a Dagger module by the engine.
  await connection(async () => {
    const outputFilePath = process.env.TYPEDEF_OUTPUT_FILE ?? "/module-id.json"
    const moduleID = await new Register(result).run()

    await fs.promises.writeFile(outputFilePath, JSON.stringify(moduleID))
  })
}

main()
