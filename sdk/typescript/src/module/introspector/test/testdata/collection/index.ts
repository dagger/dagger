import { collection, func, get, object } from "../../../../decorators.js"

@object()
export class GoTest {
  @func()
  name: string = ""
}

@object()
@collection()
export class GoTests {
  keys: string[] = []

  @get()
  lookup(name: string): GoTest {
    const test = new GoTest()
    test.name = name
    return test
  }
}

@object()
export class Collection {
  @func()
  tests(): GoTests {
    return new GoTests()
  }
}
