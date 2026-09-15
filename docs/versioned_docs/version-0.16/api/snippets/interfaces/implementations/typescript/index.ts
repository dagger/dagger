import { func, object } from "@dagger.io/dagger"

export interface Fooer {
  // You can also declare it as a method signature (e.g., `foo(): Promise<string>`)
  foo: (bar: number) => Promise<string>
}

@object()
export class Example {
  @func()
  async foo(bar: number): Promise<string> {
    return `number is: ${bar}`
  }
}
