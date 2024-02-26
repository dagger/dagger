import { object, func } from "@dagger.io/dagger"

@object()
class MyModule {

  @func()
  hello(name: string = "world"): string {
    return `Hello, ${name}`
  }

}
