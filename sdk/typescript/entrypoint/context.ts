import { Args } from "../introspector/executor/executor.js"

export type InvokeCtx = {
  parentName: string
  fnName: string
  parentArgs: Args
  fnArgs: Args
}
