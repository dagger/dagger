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
     * Directory exclusion pattern
     */
    excludeDirectory?: string,
    /**
     * File exclusion pattern
     */
    excludeFile?: string,
  ): Container {
    let filteredSource = source

    if (!excludeDirectory) {
      filteredSource = filteredSource.withoutDirectory(excludeDirectory)
    }

    if (!excludeFile) {
      filteredSource = filteredSource.withoutFile(excludeFile)
    }

    return dag.container().
      from("alpine:latest").
      withDirectory("/src", filteredSource)
  }
}
