import { object, func } from "../../../decorators/decorators.ts"
import { Lint } from "./lint.ts"
import { Test } from "./test.ts"

@object()
class MultipleObjectsAsFields {
  @func()
  test: Test = new Test()

  @func()
  lint: Lint = new Lint()

  constructor() {}
}
