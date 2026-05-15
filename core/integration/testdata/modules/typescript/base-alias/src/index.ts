import { func, object } from "@dagger.io/dagger"

@object()
export class Alias {
  @func()
  hello(): string {
    return "hello"
  }
}
