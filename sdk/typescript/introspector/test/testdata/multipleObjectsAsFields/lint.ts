import { func, object } from "../../../decorators/decorators.ts"

@object()
export class Lint {
  @func()
  echo(): string {
    return "world"
  }
}
