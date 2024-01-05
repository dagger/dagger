import { Args, registry, State } from "../introspector/registry/registry.js"

/**
 * A wrapper around the registry to invoke a function.
 *
 * @param objectName – The class to look for
 * @param methodName – The method to call in the class
 * @param state – The current state of the class
 * @param inputs – The input to send to the method to call
 */
export async function invoke(
  objectName: string,
  methodName: string,
  state: State,
  inputs: Args
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
): Promise<any> {
  return await registry.getResult(objectName, methodName, state, inputs)
}
