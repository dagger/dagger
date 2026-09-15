import { dag, Container, object, func } from "@dagger.io/dagger"

@object()
export class WrappedContainer {
  @func()
  unwrap: Container

  constructor(unwrap: Container) {
    this.unwrap = unwrap
  }

  @func()
  echo(msg: string): WrappedContainer {
    return new WrappedContainer(this.unwrap.withExec(["echo", "-n", msg]))
  }
}

@object()
export class Test {
  @func()
  container(): WrappedContainer {
    return new WrappedContainer(dag.container().from("alpine:3.22.1"))
  }
}
