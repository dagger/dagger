import { TypeDefKind } from "../api/client.gen.js"
import { Args, registry } from "../introspector/registry/registry.js"
import { Constructor } from "../introspector/scanner/abtractions/constructor.js"
import { Method } from "../introspector/scanner/abtractions/method.js"
import { DaggerModule } from "../introspector/scanner/abtractions/module.js"
import { TypeDef } from "../introspector/scanner/typeDefs.js"
import { InvokeCtx } from "./context.js"
import { loadArg, loadResult, loadInvokedMethod, loadInvokedObject } from "./load.js"

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
export async function invoke(
  module: DaggerModule,
  ctx: InvokeCtx,
): // eslint-disable-next-line @typescript-eslint/no-explicit-any
Promise<any> {
  const args: Args = {}

  let object = loadInvokedObject(module, ctx.parentName)
  if (!object) {
    throw new Error(`could not find object ${ctx.parentName}`)
  }

  const method = loadInvokedMethod(object, ctx)
  if (!method) {
    throw new Error(`could not find method ${ctx.fnName}`)
  }

  // Load arguments
  for (const argName of method.getArgOrder()) {
    const argument = method.arguments[argName]
    if (!argument) {
      throw new Error(`could not find argument ${argName}`)
    }

    const loadedArg = await loadArg(ctx.fnArgs[argName], argument.type)

    // If the argument is variadic, we need to load each args independently
    // so it's correctly propagated when it's sent to the function.
    // Note: variadic args are always last in the list of args.
    if (argument.isVariadic) {
      for (const [i, arg] of (loadedArg ?? []).entries()) {
        args[`${argName}${i}`] = arg
      }

      continue
    }

    // If the argument is nullable and the loaded arg is undefined, we set it to null.
    if (argument.isNullable && loadedArg === undefined && !argument.defaultValue) {
      args[argName] = null
      continue
    }

    args[argName] = loadedArg
  }

  // Load parent state
  for (const [key, value] of Object.entries(ctx.parentArgs)) {
    const property = object.properties[key]
    if (!property) {
      throw new Error(`could not find property ${key}`)
    }

    ctx.parentArgs[property.name] = await loadArg(value, property.type)
  }

  let result = await registry.getResult(object.name, method.name, ctx.parentArgs, args)

  if (result) {
    // Handle alias serialization by getting the return type to load
    // if the function called isn't a constructor.
    if (!isConstructor(method)) {
      const retType = method.returnType
      if (retType.kind === TypeDefKind.ObjectKind) {
        object = module.objects[(retType as TypeDef<TypeDefKind.ObjectKind>).name]
      }
    }

    result = await loadResult(result, module, object)
  }

  return result
}
