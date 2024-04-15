import { dag, Container, Directory, object } from "@dagger.io/dagger"

@object()
class MyModule {
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