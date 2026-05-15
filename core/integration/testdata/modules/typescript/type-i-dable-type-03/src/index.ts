
import { object, func } from "@dagger.io/dagger"

@object()
export class Test {
  data: string = ""

  @func()
  set(data: string): Test {
    this.data = data
    return this
  }

  @func()
  get(): string {
    return this.data
  }
}
