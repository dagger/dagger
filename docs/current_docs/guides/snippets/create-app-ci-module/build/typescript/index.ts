import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /*
   * Create a production build
   */
  @func()
  build(source: Directory): Directory {
    return dag
      .node({ ctr: this.buildBaseImage(source) })
      .commands()
      .build()
      .directory("./dist")
  }

  /*
   * Run unit tests
   */
  @func()
  async test(source: Directory): Promise<string> {
    return await dag
      .node({ ctr: this.buildBaseImage(source) })
      .commands()
      .run(["test:unit", "run"])
      .stdout()
  }

  /*
   * Build base image
   */
  buildBaseImage(source: Directory): Container {
    return dag
      .node({ version: "21" })
      .withNpm()
      .withSource(source)
      .install([])
      .container()
  }
}
