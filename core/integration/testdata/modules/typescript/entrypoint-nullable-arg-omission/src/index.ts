import { func, object } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  nullableValue(x: string | null): string {
    if (x === null) {
      return "null"
    }
    if (x === undefined) {
      return "undefined"
    }
    return x
  }
}
