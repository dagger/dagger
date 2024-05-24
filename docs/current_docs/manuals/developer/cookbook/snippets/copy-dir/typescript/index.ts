import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Return a container with a specified directory
   */
  @func()
  copyDirectory(d: Directory): Container {
    return dag.container().from("alpine:latest").withDirectory("/src", d)
  }
}
