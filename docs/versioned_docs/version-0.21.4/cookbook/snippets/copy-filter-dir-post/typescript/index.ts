import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Return a container with a filtered directory
   */
  @func()
  copyDirectoryWithExclusions(
    /**
     * Source directory
     */
    source: Directory,
    /**
     * Exclusion pattern
     */
    exclude?: string[],
  ): Container {
    return dag
      .container()
      .from("alpine:latest")
      .withDirectory("/src", source, { exclude: exclude })
  }
}
