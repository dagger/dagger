import { object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  hello(): string {
    return "Hello, world"
  }
}
