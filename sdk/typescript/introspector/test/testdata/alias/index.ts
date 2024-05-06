import { dag, Container } from "../../../../api/client.gen.js"
import { func, object, field } from "../../../decorators/decorators.js"

@object()
export class Bar {
  @field("bar")
  baz: string = "baz"

  @field("oof")
  foo: number = 4

  constructor(baz: string = "baz", foo: number = 4) {
    this.baz = baz
    this.foo = foo
  }

  @func("zoo")
  za(): string {
    return this.baz
  }
}

@object()
export class Alias {
  @field("prefix")
  gretingPrefix = "test"

  @field("container")
  ctr: Container

  constructor(ctr?: Container) {
    this.ctr = ctr ?? dag.container().from("alpine:3.14.0")
  }

  @func("testBar")
  bar(): Bar {
    return new Bar()
  }

  @func("bar")
  customBar(baz: string, foo: number): Bar {
    return new Bar(baz, foo)
  }

  @func("version")
  async displayVersion(): Promise<string> {
    return await this.ctr
      .withExec(["/bin/sh", "-c", "cat /etc/os-release | grep VERSION_ID"])
      .stdout()
  }

  @func("greet")
  helloWorld(name: string): string {
    return `hello ${name}`
  }

  @func("customGreet")
  customHelloWorld(name: string): string {
    return `${this.gretingPrefix} ${name}`
  }
}
