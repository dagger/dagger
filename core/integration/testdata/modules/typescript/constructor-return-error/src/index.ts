import { object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  foo: string

  constructor() {
    throw new Error("too bad: " + "so sad")
  }
}
