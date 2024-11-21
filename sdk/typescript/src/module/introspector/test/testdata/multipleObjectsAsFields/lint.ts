import { func, object } from "../../../../decorators/index.js"

@object()
export class Lint {
  @func()
  echo(): string {
    return "world"
  }
}
