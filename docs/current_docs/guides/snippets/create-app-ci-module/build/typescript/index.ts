import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  // create a production build
  @func()
  build(source: Directory): Directory {
    return dag
      .node()
      .withContainer(this.buildBaseImage(source))
      .build()
      .container()
      .directory("./dist")
  }

  // run unit tests
  @func()
  async test(source: Directory): Promise<string> {
    return await dag
      .node()
      .withContainer(this.buildBaseImage(source))
      .run(["run", "test:unit", "run"])
      .stdout()
  }

  // build base image
  buildBaseImage(source: Directory): Container {
    return dag
      .node()
      .withVersion("21")
      .withNpm()
      .withSource(source)
      .install([])
      .container()
  }
}
