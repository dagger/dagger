import { object, func } from "@dagger.io/dagger"

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class MyModule {

  @func()
  hello(name: string = "world"): string {
    return `Hello, ${name}`
  }

}
