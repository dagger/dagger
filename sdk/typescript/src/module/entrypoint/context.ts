import { Args } from "../executor/index.js"

export type InvokeCtx = {
  parentName: string
  fnName: string
  parentArgs: Args
  fnArgs: Args
}
