import { func, object } from "@dagger.io/dagger"

export interface Fooer {
  // You can also declare it as a method signature (e.g., `foo(): Promise<string>`)
  foo: (bar: number) => Promise<string>
}

@object()
export class MyModule {
  @func()
  async foo(fooer: Fooer): Promise<string> {
    return await fooer.foo(42)
  }
}
