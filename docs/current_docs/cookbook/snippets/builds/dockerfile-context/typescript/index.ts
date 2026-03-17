import { dag, Directory, File, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Build and publish image from existing Dockerfile. This example uses a
   * build context directory in a different location than the current working
   * directory.
   * @param src location of source directory
   * @param dockerfile location of dockerfile
   */
  @func()
  async build(src: Directory, dockerfile: File): Promise<string> {
    // get build context with Dockerfile added
    const workspace = await dag
      .container()
      .withDirectory("/src", src)
      .withWorkdir("/src")
      .withFile("/src/custom.Dockerfile", dockerfile)
      .directory("/src")

    // build using Dockerfile and publish to registry
    const ref = await workspace
      .dockerBuild({ dockerfile: "custom.Dockerfile" })
      .publish("ttl.sh/hello-dagger")

    return ref
  }
}
