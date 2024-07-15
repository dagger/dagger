import { dag, Container, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  ctr: Container = dag.container().from("alpine:3.14.0")

  constructor(ctr?: Container) {
    this.ctr = ctr ?? this.ctr
  }

  @func()
  async version(): Promise<string> {
    return await this.ctr
      .withExec(["/bin/sh", "-c", "cat /etc/os-release | grep VERSION_ID"])
      .stdout()
  }
}
