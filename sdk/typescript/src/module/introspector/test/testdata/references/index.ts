import { func, object } from "../../../../decorators.js"
import { defaultEnum, TestEnum } from "./types.js"
import type { STR, Data } from "./types.js"

/**
 * References class
 *
 * test
 */
@object()
export class References {
  @func()
  data: Data[]

  constructor(data: Data[] = []) {
    this.data = data
  }

  @func("appendData")
  addData(data: Data[]): References {
    this.data.push(...data)

    return this
  }

  @func()
  dumpDatas(): Data[] {
    return this.data
  }

  @func()
  testEnum(test: TestEnum = defaultEnum): TestEnum {
    return test
  }

  @func()
  testEnumStatic(test: TestEnum = TestEnum.A): TestEnum {
    return test
  }

  /**
   * Doc
   *
   * foo
   */
  @func()
  async testDefaultValue(
    /**
     * Doc
     *
     * test
     */
    foo: STR = "a",
  ): Promise<STR> {
    return foo
  }
}
