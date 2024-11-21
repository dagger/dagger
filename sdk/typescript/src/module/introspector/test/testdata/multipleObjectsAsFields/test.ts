import { func, object } from "../../../../decorators/index.js"

@object()
export class Test {
  @func()
  echo(): string {
    return "world"
  }
}
