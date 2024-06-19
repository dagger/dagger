import { dag, Container, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * The greeting to use
   */
  @func()
  greeting: string

  /**
   * Who to greet
   */
  name: string

  constructor(
    /**
     * The greeting to use
     */
    greeting: string = "Hello",
    /**
     * Who to greet
     */
    name: string = "World"
  ) {
    this.greeting = greeting
    this.name = name
  }

  /**
   * Return the greeting message
   */
  @func()
  message(): string {
    return `${this.greeting}, ${this.name}!`
  }
}
