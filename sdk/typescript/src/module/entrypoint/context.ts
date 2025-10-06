import { Args } from "../executor.js"

export type InvokeCtx = {
  parentName: string
  fnName: string
  parentArgs: Args
  fnArgs: Args
}
