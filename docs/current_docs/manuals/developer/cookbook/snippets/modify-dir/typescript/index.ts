import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Returns a container with a specified directory and an additional file
   */
  @func()
  modifyDirectory(dir: Directory): Container {
    return dag.container().from("alpine:latest")
      .withDirectory("/src", dir)
      .withExec(["/bin/sh", "-c", "`echo foo > /src/foo`"])
  }
}
