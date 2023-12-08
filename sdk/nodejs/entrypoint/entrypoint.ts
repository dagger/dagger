import * as path from "path"
import { fileURLToPath } from "url"

import { dag } from "../api/client.gen.js"
import { connection } from "../connect.js"
import { Args } from "../introspector/registry/registry"
import { listFiles } from "../introspector/utils/files.js"
import { invoke } from "./invoke.js"
import { load, loadArg } from "./load.js"
import { register } from "./register.js"

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

const moduleSrcDirectory = `${__dirname}/../../src/`

async function entrypoint() {
  // Pre list all files of the modules since we need it either for a registration
  // or an invocation
  const files = await listFiles(moduleSrcDirectory)

  // Start a Dagger session to get the call context
  await connection(
    async () => {
      const fnCall = dag.currentFunctionCall()

      const parentName = await fnCall.parentName()

      // Pre allocate the result, we got one in both case.
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      let result: any

      if (parentName === "") {
        // It's a registration, we register the module and assign the module id
        // to the result
        result = await register(files)
      } else {
        // Invocation
        const fnName = await fnCall.name()
        const parentJson = await fnCall.parent()
        const fnArgs = await fnCall.inputArgs()

        const args: Args = {}

        for (const arg of fnArgs) {
          args[await arg.name()] = await loadArg(await arg.value())
        }

        await load(files)

        result = await invoke(parentName, fnName, JSON.parse(parentJson), args)

        // Load ID if it's a Dagger type with an id
        if (result.id && typeof result.id === "function") {
          result = await result.id()
        }
      }

      // Send the result to Dagger
      await fnCall.returnValue(
        JSON.stringify(result) as string & { __JSON: never }
      )
    },
    { LogOutput: process.stdout }
  )
}

entrypoint()
