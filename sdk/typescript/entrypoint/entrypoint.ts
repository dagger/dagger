import * as path from "path"
import { fileURLToPath } from "url"

import { dag } from "../api/client.gen.ts"
import { connection } from "../connect.ts"
import { Args } from "../introspector/registry/registry.ts"
import { scan } from "../introspector/scanner/scan.ts"
import { listFiles } from "../introspector/utils/files.ts"
import { invoke } from "./invoke.ts"
import { load } from "./load.ts"
import { register } from "./register.ts"

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

const moduleSrcDirectory = `${__dirname}/../../src/`

export async function entrypoint() {
  // Pre list all files of the modules since we need it either for a registration
  // or an invocation
  const files = await listFiles(moduleSrcDirectory)

  // Start a Dagger session to get the call context
  await connection(
    async () => {
      const fnCall = dag.currentFunctionCall()
      const moduleName = await dag.currentModule().name()
      const parentName = await fnCall.parentName()

      const scanResult = scan(files, moduleName)

      // Pre allocate the result, we got one in both case.
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      let result: any

      if (parentName === "") {
        result = await register(files, scanResult)
      } else {
        // Invocation
        const fnName = await fnCall.name()
        const parentJson = JSON.parse(await fnCall.parent())
        const fnArgs = await fnCall.inputArgs()

        const args: Args = {}
        const parentArgs: Args = parentJson ?? {}

        for (const arg of fnArgs) {
          args[await arg.name()] = JSON.parse(await arg.value())
        }

        await load(files)

        try {
          result = await invoke(scanResult, {
            parentName,
            fnName,
            parentArgs,
            fnArgs: args,
          })
        } catch (e) {
          if (e instanceof Error) {
            if (e.cause) {
              console.error(`${e.cause}`)
            }
            console.error(`Error: ${e.message}`)
          } else {
            console.error(e)
          }
          process.exit(1)
        }
      }

      // If result is set, we stringify it
      if (result !== undefined && result !== null) {
        result = JSON.stringify(result)
      } else {
        result = "null"
      }

      // Send the result to Dagger
      await fnCall.returnValue(result as string & { __JSON: never })
    },
    { LogOutput: process.stdout },
  )
}
