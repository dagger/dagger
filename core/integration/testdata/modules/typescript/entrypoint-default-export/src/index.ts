import { func, object } from "@dagger.io/dagger"

@object()
export default class Test {
  @func()
  hello(): string {
    return "hello"
  }
}
