import { func, object } from "../../../decorators/decorators.ts"

@object()
class Integer {
  @func()
  value: number

  constructor(value: number) {
    this.value = value
  }

  @func()
  positive(): boolean {
    return this.value > 0
  }
}

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class List {
  @func()
  create(...n: number[]): Integer[] {
    return n.map((v) => new Integer(v))
  }
}
