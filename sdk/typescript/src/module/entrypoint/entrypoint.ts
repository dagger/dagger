import { dag } from "../../api/client.gen.js"
import { connection } from "../../connect.js"
import { Executor, Args } from "../executor.js"
import { scan } from "../introspector/index.js"
import { invoke } from "./invoke.js"
import { load } from "./load.js"
import { register } from "./register.js"

export async function entrypoint(files: string[]) {
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

        const modules = await load(files)
        const executor = new Executor(modules, scanResult)

        try {
          result = await invoke(executor, scanResult, {
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
