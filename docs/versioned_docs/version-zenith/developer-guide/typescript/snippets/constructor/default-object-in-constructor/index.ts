import { dag, Container, object, func, field } from "@dagger.io/dagger"

@object()
class MyModule {
  @field()
  ctr: Container

  constructor(ctr?: Container) {
    this.ctr = ctr ?? dag.container().from("alpine:3.14.0")
  }

  @func()
  async version(): Promise<string> {
    return await this.ctr.withExec(["/bin/sh", "-c", "cat /etc/os-release | grep VERSION_ID"]).stdout()
  }
}
