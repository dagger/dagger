import { func, object } from "@dagger.io/dagger"

export interface Fooer {
  foo(bar: number): Promise<string>
}

@object()
export class MyModule {
  @func()
  async foo(fooer: Fooer): Promise<string> {
    return await fooer.foo(42)
  }
}
