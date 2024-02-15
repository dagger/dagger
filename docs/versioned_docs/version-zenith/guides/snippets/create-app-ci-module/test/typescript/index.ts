import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class MyModule {

  source: Directory

  // constructor
  constructor (source: Directory) {
    this.source = source
  }

  // run unit tests
  @func()
  async test(): Promise<string> {
    return await dag.node().withContainer(this.buildBaseImage())
      .run(["run", "test:unit", "run"])
      .stdout()
  }

  // build base image
  buildBaseImage(): Container {
    return dag.node()
      .withVersion("21")
      .withNpm()
      .withSource(this.source)
      .install([])
      .container()
  }

}
