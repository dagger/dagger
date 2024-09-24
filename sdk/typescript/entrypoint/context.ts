import { Args } from "../introspector/registry/registry.ts"

export type InvokeCtx = {
  parentName: string
  fnName: string
  parentArgs: Args
  fnArgs: Args
}
