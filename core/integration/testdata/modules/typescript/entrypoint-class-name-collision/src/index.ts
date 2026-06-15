import { func, object } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  makeContext(): Context {
    return new Context()
  }
}

@object()
export class Context {
  @func()
  value(): string {
    return "ok"
  }
}
