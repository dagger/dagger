import { object, func } from "@dagger.io/dagger"

@object()
class Test {
  @func()
  id(): string {
    return "NOOOO!!!!"
  }
}
