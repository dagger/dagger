import { func, object } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  hello(): string {
    return "hello"
  }
}
