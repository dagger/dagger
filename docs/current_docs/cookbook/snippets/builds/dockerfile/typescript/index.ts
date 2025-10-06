import { dag, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Build and publish image from existing Dockerfile
   * @param src location of directory containing Dockerfile
   */
  @func()
  async build(src: Directory): Promise<string> {
    const ref = await src
      .dockerBuild() // build from Dockerfile
      .publish("ttl.sh/hello-dagger")

    return ref
  }
}
