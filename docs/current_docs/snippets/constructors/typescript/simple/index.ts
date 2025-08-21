import { object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  greeting: string
  name: string

  constructor(greeting = "Hello", name = "World") {
    this.greeting = greeting
    this.name = name
  }

  @func()
  message(): string {
    return `${this.greeting} ${this.name}`
  }
}
