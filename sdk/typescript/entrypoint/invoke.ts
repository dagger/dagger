import { Args, registry } from "../introspector/registry/registry.js"
import { ScanResult } from "../introspector/scanner/scan.js"
import {
  loadArgOrder,
  loadArg,
  loadArgType,
  loadPropertyType,
  loadResult,
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
    args[argName] = await loadArg(
      fnArgs[argName],
      loadArgType(scanResult, parentName, fnName, argName)
    )
  }

  // Load parent state
  for (const [key, value] of Object.entries(parentArgs)) {
    parentArgs[key] = await loadArg(
      value,
      loadPropertyType(scanResult, parentName, key)
    )
  }

  let result = await registry.getResult(parentName, fnName, parentArgs, args)
  if (result) {
    result = await loadResult(result)
  }

  return result
}
