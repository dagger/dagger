import { object, field, func } from "@dagger.io/dagger"

/**
 * The object represents a single user of the system.
 */
@object()
class MyModule {
  /**
   * The name of the user.
   */
  @field()
  name: string

  /**
   * The age of the user.
   */
  @field()
  age: number

  constructor(age: number, name: string) {
    this.name = name
    this.age = age
  }
}
