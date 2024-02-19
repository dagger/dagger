import { dag, object, func, field } from "@dagger.io/dagger"

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class HelloWorld {

  greeting: string
  name: string

  constructor (greeting: string = "Hello", name: string = "World") {
    this.greeting = greeting
    this.name = name
  }

  @func()
  message(): string {
    return `${this.greeting} ${this.name}`
  }
}
