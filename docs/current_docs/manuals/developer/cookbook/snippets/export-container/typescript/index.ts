import { dag, Directory, Container, File, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Return a container
   */
  @func()
  base(): Container {
    return dag.container()
      .from("alpine:latest")
      .withExec(["mkdir", "/src"])
      .withExec(["touch", "/src/foo", "/src/bar"])
  }
}
