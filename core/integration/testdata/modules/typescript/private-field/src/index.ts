import { object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  foo: string

  bar?: string

  constructor(foo?: string, bar?: string) {
    this.foo = foo
    this.bar = bar
  }

  @func()
  set(foo: string, bar: string): Test {
    this.foo = foo
    this.bar = bar
    return this
  }

  @func()
  hello(): string {
    return this.foo + this.bar
  }
}
