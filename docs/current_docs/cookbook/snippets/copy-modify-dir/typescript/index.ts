import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Return a container with a specified directory and an additional file
   */
  @func()
  copyAndModifyDirectory(
    /**
     * Source directory
     */
    source: Directory,
  ): Container {
    return dag
      .container()
      .from("alpine:latest")
      .withDirectory("/src", source)
      .withExec(["/bin/sh", "-c", "`echo foo > /src/foo`"])
  }
}
