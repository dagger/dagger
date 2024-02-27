import { object, func } from "@dagger.io/dagger"

@object()
class Potato {
  @func()
  helloWorld(): string {
    return "Hello Daggernauts!"
  }
}
