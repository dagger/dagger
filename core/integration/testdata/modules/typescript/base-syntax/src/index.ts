import { func, object } from "@dagger.io/dagger"

@object()
export class Syntax {
  @func()
  hello(): string {
    return "hello"
  }
}
