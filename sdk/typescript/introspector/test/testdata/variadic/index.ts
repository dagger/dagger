import { func, object } from "../../../decorators/decorators.js"

@object()
export class Variadic {
  @func()
  fullVariadicStr(...vars: string[]): string {
    return `hello ${vars.join(" ")}`
  }

  @func()
  semiVariadicStr(separator: string, ...vars: string[]): string {
    return `hello ${vars.join(separator)}`
  }

  @func()
  fullVariadicNum(...vars: number[]): number {
    return vars.reduce((a, b) => a + b)
  }

  @func()
  semiVariadicNum(mul: number, ...vars: number[]): number {
    return vars.reduce((a, b) => a + b, 0) * mul
  }
}
