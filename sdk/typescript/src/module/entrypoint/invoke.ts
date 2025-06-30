import { TypeDefKind } from "../../api/client.gen.js"
import { FunctionNotFound } from "../../common/errors/index.js"
import { Executor } from "../executor.js"
import {
  DaggerConstructor as Constructor,
  DaggerFunction as Method,
  DaggerEnumBase,
  DaggerModule,
  DaggerObjectBase,
} from "../introspector/dagger_module/index.js"
import { registry } from "../registry.js"
import { InvokeCtx } from "./context.js"
import {
  loadResult,
  loadInvokedMethod,
  loadInvokedObject,
  loadArgs,
  loadParentState,
  loadObjectReturnType,
} from "./load.js"

function isConstructor(method: Method | Constructor): method is Constructor {
  return method.name === ""
}

function isFloat(num: number): boolean {
  return num % 1 !== 0
}

/**
 * A wrapper around the registry to invoke a function.
 *
 * @param scanResult The result of the scan.
 * @param parentName The name of the parent object.
 * @param fnName The name of the function to call.
 * @param parentArgs The arguments of the parent object.
 * @param fnArgs The arguments of the function to call.
 */
export async function invoke(
  executor: Executor,
  module: DaggerModule,
  ctx: InvokeCtx,
) {
  const object = loadInvokedObject(module, ctx.parentName)
  if (!object) {
    throw new Error(`could not find object ${ctx.parentName}`)
  }

  const method = loadInvokedMethod(object, ctx)
  if (!method) {
    throw new Error(`could not find method ${ctx.fnName}`)
  }

  const args = await loadArgs(executor, method, ctx)
  const parentState = await loadParentState(executor, object, ctx)

  // Disabling linter because the result could be anything.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  let result: any = {}

  try {
    result = await executor.getResult(
      object.name,
      method.name,
      parentState,
      args,
    )
  } catch (e) {
    // If the function isn't found because it's
    // not exported, we try to get the result from the registry.
    if (e instanceof FunctionNotFound) {
      result = await registry.getResult(
        object.name,
        method.name,
        parentState,
        args,
      )
    } else {
      throw e
    }
  }

  if (result) {
    let returnType: DaggerObjectBase | DaggerEnumBase

    // Handle alias serialization by getting the return type to load
    // if the function called isn't a constructor.
    if (!isConstructor(method)) {
      // Guard to catch if a user returned a float number but the function's return
      // type is set to `number` (integer).
      if (
        method.returnType!.kind === TypeDefKind.IntegerKind &&
        isFloat(result)
      ) {
        throw new Error(
          `cannot return float '${result}' if return type is 'number' (integer), please use 'float' as return type instead`,
        )
      }

      returnType = loadObjectReturnType(module, object, method)
    } else {
      returnType = object
    }

    result = await loadResult(result, module, returnType)
  }

  return result
}
