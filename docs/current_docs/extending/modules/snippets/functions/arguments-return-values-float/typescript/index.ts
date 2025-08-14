import type { float } from "@dagger.io/dagger"
import { object, func } from "@dagger.io/dagger"

@object()
export class MyModule {
  @func()
  addFloat(a: float, b: float): float {
    return a + b
  }
}
