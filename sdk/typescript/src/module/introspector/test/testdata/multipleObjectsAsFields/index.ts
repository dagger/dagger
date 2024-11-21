import { object, func } from "../../../../decorators/index.js"
import { Lint } from "./lint.js"
import { Test } from "./test.js"

@object()
export class MultipleObjectsAsFields {
  @func()
  test: Test = new Test()

  @func()
  lint: Lint = new Lint()

  constructor() {}
}
