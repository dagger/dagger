import { object, func } from "../../../decorators/decorators.js"
import { Lint } from "./lint.js"
import { Test } from "./test.js"

@object()
class MultipleObjectsAsFields {
  @func()
  test: Test = new Test()

  @func()
  lint: Lint = new Lint()

  constructor() {}
}
