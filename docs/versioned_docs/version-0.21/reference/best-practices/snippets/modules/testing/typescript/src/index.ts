import { object, func } from "@dagger.io/dagger"

@object()
export class Greeter {
  greeting: string

  constructor(greeting = "Hello") {
    this.greeting = greeting
  }

  /**
   * Greets the provided name.
   */
  @func()
  hello(name: string): string {
    return `${this.greeting}, ${name}!`
  }
}
