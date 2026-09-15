import { object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  id(): string {
    return "NOOOO!!!!"
  }
}
