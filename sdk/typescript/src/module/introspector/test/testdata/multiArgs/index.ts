import { func, object } from "../../../../decorators.js"

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
export class MultiArgs {
  @func()
  compute(a: number, b: number, c: number): number {
    return a * b + c
  }
}
