import { dag, Container, Directory, object } from "@dagger.io/dagger";

@object()
class MyModule {
  // build base image
  buildBaseImage(source: Directory): Container {
    return dag
      .node()
      .withVersion("21")
      .withNpm()
      .withSource(source)
      .install([])
      .container();
  }
}
