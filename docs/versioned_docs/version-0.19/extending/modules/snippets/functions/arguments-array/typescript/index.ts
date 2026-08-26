import { object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  hello(names: string[]): string {
    let message = "Hello"
    for (const name of names) {
      message += ` ${name}`
    }
    return message
  }
}
