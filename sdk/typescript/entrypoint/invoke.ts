import { registry } from "../introspector/registry/registry.js"
import { Constructor } from "../introspector/scanner/abtractions/constructor.js"
import { DaggerEnum } from "../introspector/scanner/abtractions/enum.js"
import { Method } from "../introspector/scanner/abtractions/method.js"
import { DaggerModule } from "../introspector/scanner/abtractions/module.js"
import { DaggerObject } from "../introspector/scanner/abtractions/object.js"
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

/**
 * A wrapper around the registry to invoke a function.
 *
 * @param scanResult The result of the scan.
 * @param parentName The name of the parent object.
 * @param fnName The name of the function to call.
 * @param parentArgs The arguments of the parent object.
 * @param fnArgs The arguments of the function to call.
 */
export async function invoke(module: DaggerModule, ctx: InvokeCtx) {
  const object = loadInvokedObject(module, ctx.parentName)
  if (!object) {
    throw new Error(`could not find object ${ctx.parentName}`)
  }

  const method = loadInvokedMethod(object, ctx)
  if (!method) {
    throw new Error(`could not find method ${ctx.fnName}`)
  }

  const args = await loadArgs(registry, method, ctx)
  const parentState = await loadParentState(registry, object, ctx)

  let result = await registry.getResult(
    object.name,
    method.name,
    parentState,
    args,
  )

  if (result) {
    let returnType: DaggerObject | DaggerEnum

    // Handle alias serialization by getting the return type to load
    // if the function called isn't a constructor.
    if (!isConstructor(method)) {
      returnType = loadObjectReturnType(module, object, method)
    } else {
      returnType = object
    }

    result = await loadResult(result, module, returnType)
  }

  return result
}
