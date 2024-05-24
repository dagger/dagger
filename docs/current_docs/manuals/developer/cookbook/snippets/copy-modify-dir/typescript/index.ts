import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Return a container with a specified directory and an additional file
   */
  @func()
  copyAndModifyDirectory(d: Directory): Container {
    return dag
      .container()
      .from("alpine:latest")
      .withDirectory("/src", d)
      .withExec(["/bin/sh", "-c", "`echo foo > /src/foo`"])
  }
}
