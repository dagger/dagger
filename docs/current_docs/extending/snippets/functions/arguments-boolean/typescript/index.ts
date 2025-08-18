import { object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  hello(shout: boolean): string {
    const message = "Hello, world"
    if (shout) {
      return message.toUpperCase()
    }
    return message
  }
}
