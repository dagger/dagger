import { dag, Container, Directory, object } from "@dagger.io/dagger"

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class MyModule {

  source: Directory

  // constructor
  constructor (source: Directory) {
    this.source = source
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
