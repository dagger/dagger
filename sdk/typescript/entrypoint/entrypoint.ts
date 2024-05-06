import * as path from "path"
import { fileURLToPath } from "url"

import { dag } from "../api/client.gen.js"
import { connection } from "../connect.js"
import { Args } from "../introspector/registry/registry"
import { scan } from "../introspector/scanner/scan.js"
import { listFiles } from "../introspector/utils/files.js"
import { UI_MASK, UI_PASSTHROUGH } from "../telemetry/attributes.js"
import { getTracer } from "../telemetry/index.js"
import { invoke } from "./invoke.js"
import { load } from "./load.js"
import { register } from "./register.js"

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

      const scanResult = await scan(files, moduleName)

      // Pre allocate the result, we got one in both case.
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      let result: any

      if (parentName === "") {
        result = await getTracer().startActiveSpan(
          "typescript module registration",
          async () => {
            return await register(files, scanResult)
          },
          { [UI_MASK]: true },
        )
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
          result = await getTracer().startActiveSpan(
            "typescript module execution",
            async () => {
              return await invoke(scanResult, {
                parentName,
                fnName,
                parentArgs,
                fnArgs: args,
              })
            },
            {
              [UI_MASK]: true,
              [UI_PASSTHROUGH]: true,
            },
          )
        } catch (e) {
          if (e instanceof Error) {
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
