import { object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  hello(shout: boolean): string {
    let message = "Hello, world"
    if (shout) {
      return message.toUpperCase()
    }
    return message
  }
}
