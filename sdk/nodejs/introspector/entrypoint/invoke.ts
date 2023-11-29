import { registry, State } from "../registry/registry.js"

export async function invoke(
  objectName: string,
  methodName: string,
  state: State,
  inputs: unknown[]
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
): Promise<any> {
  return await registry.getResult(objectName, methodName, state, inputs)
}
