import { func, object } from "@dagger.io/dagger"

@object()
export class Dep {
  @func()
  hello(): string {
    return "hello"
  }
}
