import { object, field } from "@dagger.io/dagger"

/**
 * The Person object represents a single user of the system
 */
@object()
class Person {
  /**
   * The name of the person.
   */
  @field()
  name = "anonymous"

  /**
   * The age of the person.
   */
  @field()
  age: number

  constructor(age: number, name?: string) {
    this.name = name ?? this.name
    this.age = age
  }
}
