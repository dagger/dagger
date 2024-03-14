import { object, func, field } from "@dagger.io/dagger"

@object()
class MyModule {
  @field()
  greeting = "Hello"

  @field()
  name = "World"

  @func()
  withGreeting(greeting: string): MyModule {
    this.greeting = greeting
    return this
  }

  @func()
  withName(name: string): MyModule {
    this.name = name
    return this
  }

  @func()
  message(): string {
    return `${this.greeting} ${this.name}`
  }
}
