import { func, object } from "../../../../decorators.js"

@object()
export class Lint {
  @func()
  echo(): string {
    return "world"
  }
}
