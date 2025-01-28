import type { float } from "../../../../../api/client.gen.js"
import { func, object } from "../../../../decorators.js"

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
export class MultiArgs {
  @func()
  compute(a: number, b: number, c: float): float {
    return a * b + c
  }
}
