import { func, object } from '../../../decorators/decorators.js'

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class HelloWorld {
  @func()
  compute(a: number, b: number, c: number): number {
    return a * b + c
  }
}