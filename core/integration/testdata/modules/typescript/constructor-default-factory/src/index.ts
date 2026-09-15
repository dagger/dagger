import { dag, File, object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  foo: File = dag
    .directory()
    .withNewFile("foo.txt", "default factory content")
    .file("foo.txt")

  @func()
  bar: string[] = []

  constructor(foo?: File) {
    if (foo) {
      this.foo = foo
    }
  }
}
