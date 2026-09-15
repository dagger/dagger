
import { dag, Container, File, object, func } from "@dagger.io/dagger"

@object()
export class Foo {
  @func()
  con: Container

  @func()
  unsetFile?: File

  constructor(con: Container, unsetFile?: File) {
    this.con = con
    this.unsetFile = unsetFile
  }
}

@object()
export class Test {
  @func()
  mySlice(): Container[] {
    return [
      dag.container().from("alpine:3.22.1").withExec(["echo", "hello world"])
    ]
  }

  @func()
  myStruct(): Foo {
    return new Foo(
      dag.container().from("alpine:3.22.1").withExec(["echo", "hello world"])
    )
  }
}
