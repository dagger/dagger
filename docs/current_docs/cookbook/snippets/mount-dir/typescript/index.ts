import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Return a container with a mounted directory
   */
  @func()
  mountDirectory(
    /**
     * Source directory
     */
    source: Directory,
  ): Container {
    return dag
      .container()
      .from("alpine:latest")
      .withMountedDirectory("/src", source)
  }
}
