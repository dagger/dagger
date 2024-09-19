import { func, object } from "../../../decorators/decorators.ts"

@object()
export class Test {
  @func()
  echo(): string {
    return "world"
  }
}
