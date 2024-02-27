import { dag, Container, object, func, field } from "@dagger.io/dagger"

@object()
class Alpine {
  @field()
  ctr: Container

  constructor(version = "3.14") {
    this.ctr = dag.container().from(`alpine:${version}`)
  }

  @func()
  async echo(msg: string[]): Promise<string> {
    return this.ctr.withExec(["echo", ...msg]).stdout()
  }
}

@object()
class MyModule {
  @func()
  alpine(version?: string): Alpine {
    return new Alpine(version)
  }
}
