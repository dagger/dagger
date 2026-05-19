import { func, object } from "@dagger.io/dagger"

@object()
export class Minimal {
  @func()
  hello(): string {
    return "hello"
  }
}
