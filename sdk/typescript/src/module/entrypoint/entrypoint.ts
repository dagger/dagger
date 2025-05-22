import { dag } from "../../api/client.gen.js"
import { connection } from "../../connect.js"
import { Executor, Args } from "../executor.js"
import { scan } from "../introspector/index.js"
import { invoke } from "./invoke.js"
import { load } from "./load.js"
import { register } from "./register.js"

export async function entrypoint(files: string[]) {
  // Start a Dagger session to get the cƒall context
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
        } catch (e: unknown) {
          await fnCall.returnError(dag.error(stringifyError(e)))
          return
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

/**
 * Take the error thrown by the user module and stringify it so it can
 * be returned to Dagger.
 *
 * If the error is an instande of Error, we stringify the message and the cause.
 * If the error is not an instance of Error (which is the case for unexpected errors happening
 * inside the code), we try to stringify it, if it fails we convert it to a string.
 *
 * Hopefully, this will be enough to make the error readable by the user.
 * If the stringify fails, it will fails when calling `returnError` and should
 * still be displayed to the user and Cloud.
 */
function stringifyError(e: unknown): string {
  if (e instanceof Error) {
    let error = `${e.name}: ${e.message}`
    if (e.cause) {
      error += `: ${e.cause}`
    }

    return error
  }

  try {
    return JSON.stringify(e)
  } catch {
    return String(e)
  }
}
