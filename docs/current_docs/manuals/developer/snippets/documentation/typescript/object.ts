import { object, func } from "@dagger.io/dagger"

/**
 * The object represents a single user of the system.
 */
@object()
class MyModule {
  @func()
  name: string
  @func()
  age: number

  constructor(
    /**
     * The name of the user.
     */
    age: number,
    /**
     * The age of the user.
     */
    name: string
  ) {
    this.name = name
    this.age = age
  }
}
