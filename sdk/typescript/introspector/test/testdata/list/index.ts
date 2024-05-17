import { func, object, field } from "../../../decorators/decorators.js"

@object()
class Number {
  @field()
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
  create(...n: number[]): Number[] {
    return n.map((v) => new Number(v))
  }
}
