import { func, object } from "../../../../decorators.js"

@object()
export class Test {
  @func()
  echo(): string {
    return "world"
  }
}
