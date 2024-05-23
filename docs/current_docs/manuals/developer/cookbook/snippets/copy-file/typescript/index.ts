import { dag, Container, File, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Returns a container with a specified file
   */
  @func()
  async copyFile(f: File): Promise<Container> {
    const name = await f.name()
    return dag.container().from("alpine:latest").withFile(`/src/${name}`, f)
  }
}
