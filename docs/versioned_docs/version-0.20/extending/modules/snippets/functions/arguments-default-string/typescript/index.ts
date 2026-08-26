import { object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  hello(name = "world"): string {
    return `Hello, ${name}`
  }
}
