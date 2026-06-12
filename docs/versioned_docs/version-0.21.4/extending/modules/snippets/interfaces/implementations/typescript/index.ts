import { func, object } from "@dagger.io/dagger"

export interface Fooer {
  foo(bar: number): Promise<string>
}

@object()
export class Example implements Fooer {
  @func()
  async foo(bar: number): Promise<string> {
    return `number is: ${bar}`
  }
}
