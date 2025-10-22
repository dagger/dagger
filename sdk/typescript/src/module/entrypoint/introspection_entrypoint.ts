import * as fs from "fs"
import * as path from "path"

import { connection } from "../../connect.js"
import { scan } from "../introspector/index.js"
import { Register } from "./register.js"

async function introspection(files: string[], moduleName: string) {
  return await scan(files, moduleName, false)
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
    .reduce((p, c) => [...c, ...p])
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

  const result = await introspection(
    [...userSourceCodeFiles, typescriptClientFile],
    moduleName,
  )

  if (process.env.DRY_RUN) {
    console.log(JSON.stringify(result, null, 2))
    process.exit(0)
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
