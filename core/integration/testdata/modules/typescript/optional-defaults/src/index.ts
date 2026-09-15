import { object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  foo(
    a: string,
    b?: string,
    c: string = "foo",
    d: string | null = null,
    e: string | null = "bar",
  ): string {
    return [a, b, c, d, e].map((v) => JSON.stringify(v)).join(", ")
  }
}
