import { dag, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Build and publish image with OCI annotations
   */
  @func()
  async build(): Promise<string> {
    const address = await dag
      .container()
      .from("alpine:latest")
      .withExec(["apk", "add", "git"])
      .withWorkdir("/src")
      .withExec(["git", "clone", "https://github.com/dagger/dagger", "."])
      .withAnnotation("org.opencontainers.image.authors", "John Doe")
      .withAnnotation(
        "org.opencontainers.image.title",
        "Dagger source image viewer",
      )
      .publish(`ttl.sh/custom-image-${Math.floor(Math.random() * 10000000)}`)

    return address
  }
}
