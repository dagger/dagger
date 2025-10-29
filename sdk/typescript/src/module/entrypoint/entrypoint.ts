import { dag, Error as DaggerError } from "../../api/client.gen.js"
import type { JSON } from "../../api/client.gen.js"
import { ExecError } from "../../common/errors/ExecError.js"
import { GraphQLRequestError } from "../../common/errors/GraphQLRequestError.js"
import { connection } from "../../connect.js"
import * as telemetry from "../../telemetry/telemetry.js"
import { Executor, Args } from "../executor.js"
import { scan } from "../introspector/index.js"
import { invoke } from "./invoke.js"
import { load } from "./load.js"
import { Register } from "./register.js"

export async function entrypoint(files: string[]) {
  // Start a Dagger session to get the call context
  await connection(
    async () => {
      const fnCall = dag.currentFunctionCall()
      const moduleName = await dag.currentModule().name()
      const scanResult = await scan(files, moduleName)
      const parentName = await fnCall.parentName()

      // Pre allocate the result, we got one in both case.
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      let result: any

      if (parentName === "") {
        result = await new Register(scanResult).run()
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
          await fnCall.returnError(formatError(e))

          // Closing telemetry here since the process is shutdown after
          // so any parent's finally will not be executed.
          // That way, we guarantee that the spans are flushed.
          await telemetry.close()

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

/**
 * Take the error thrown by the user module and stringify it so it can
 * be returned to Dagger.
 *
 * If the error is an instance of Error, we stringify the message.
 * If the error is an instance of ExecError, we stringify the message and add the
 * extensions fields in the error object.
 */
function formatError(e: unknown): DaggerError {
  if (e instanceof Error) {
    let error = dag.error(e.message)

    // If the error is an instance of GraphQLError or a inherit type of it,
    // we can add the extensions fields in the error object.
    if (e instanceof ExecError || e instanceof GraphQLRequestError) {
      Object.entries(e.extensions ?? []).forEach(([key, value]) => {
        if (value !== "" && value !== undefined && value !== null) {
          error = error.withValue(key, JSON.stringify(value) as JSON)
        }
      })
    }

    return error
  }

  try {
    return dag.error(JSON.stringify(e))
  } catch {
    return dag.error(String(e))
  }
}
