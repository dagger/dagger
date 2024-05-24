import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Build an application using cached dependencies
   */
  @func()
  build(source: Directory): Container {
    return dag.container()
      .from("node:21")
      .withDirectory("/src", source)
      .withWorkdir("/src")
      .withMountedCache(
        "/src/node_modules",
        dag.cacheVolume("node-21-myapp-myenv"),
      )
      .withMountedCache(
        "/root/.npm", dag.cacheVolume("node-21")
      )
      .withExec(["npm", "install"])
  }
}
