import { dag, object, func, field } from "@dagger.io/dagger"

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class HelloWorld {

  @field()
  greeting = "Hello"

  @field()
  name = "World"

  @func()
  withGreeting(greeting: string): HelloWorld {
    this.greeting = greeting
    return this
  }

  @func()
  withName(name: string): HelloWorld {
    this.name = name
    return this
  }

  @func()
  message(): string {
    return `${this.greeting} ${this.name}`
  }
}
