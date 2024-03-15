import { object, field } from "../../../decorators/decorators.js"
import { Lint } from './lint.js';
import { Test } from './test.js'

@object()
class MultipleObjectsAsFields {
  @field()
  test: Test = new Test()

  @field()
  lint: Lint = new Lint()

  constructor() {}
}