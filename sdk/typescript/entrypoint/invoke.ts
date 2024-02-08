import { TypeDefKind } from "../api/client.gen.js"
import { Args, registry } from "../introspector/registry/registry.js"
import { ScanResult } from "../introspector/scanner/scan.js"
import { TypeDef } from "../introspector/scanner/typeDefs.js"
import {
  loadArgOrder,
  loadArg,
  loadArgType,
  loadPropertyType,
  loadResult,
  isArgVariadic,
  loadName,
} from "./load.js"

export type InvokeCtx = {
  parentName: string
  fnName: string
  parentArgs: Args
  fnArgs: Args
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
  scanResult: ScanResult,
  { parentName, fnName, parentArgs, fnArgs }: InvokeCtx
): // eslint-disable-next-line @typescript-eslint/no-explicit-any
Promise<any> {
  const args: Args = {}

  // Load function arguments in the right order
  for (const argName of loadArgOrder(scanResult, parentName, fnName)) {
    const loadedArg = await loadArg(
      fnArgs[argName],
      loadArgType(scanResult, parentName, fnName, argName)
    )

    if (isArgVariadic(scanResult, parentName, fnName, argName)) {
      // If the argument is variadic, we need to load each args independently
      // so it's correctly propagated when it's sent to the function.
      // Note: variadic args are always last in the list of args.
      for (const [i, arg] of (loadedArg ?? []).entries()) {
        args[`${argName}${i}`] = arg
      }
    } else {
      args[argName] = loadedArg
    }
  }

  // Load parent state
  for (const [key, value] of Object.entries(parentArgs)) {
    parentArgs[loadName(scanResult, parentName, key, "field")] = await loadArg(
      value,
      loadPropertyType(scanResult, parentName, key)
    )
  }

  let result = await registry.getResult(
    loadName(scanResult, parentName, parentName, "object"),
    loadName(scanResult, parentName, fnName, "function"),
    parentArgs,
    args
  )

  if (result) {
    // Handle alias serialization by getting the return type to load
    // if the function called isn't a constructor.
    if (fnName !== "") {
      const retType = scanResult.classes[parentName].methods[fnName].returnType
      if (retType.kind === TypeDefKind.ObjectKind) {
        parentName = (retType as TypeDef<TypeDefKind.ObjectKind>).name
      }
    }

    result = await loadResult(result, scanResult, parentName)
  }

  return result
}
