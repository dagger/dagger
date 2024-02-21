import { object, func } from "@dagger.io/dagger"

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class MyModule {

  @func()
  hello(): string {
    return "Hello, world"
  }

}
