import { dag, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Build and publish image with oci labels
   */
  @func()
  async build(): Promise<string> {
    const ref = await dag
      .container()
      .from("alpine")
      .withLabel("org.opencontainers.image.title", "my-alpine")
      .withLabel("org.opencontainers.image.version", "1.0")
      .withLabel("org.opencontainers.image.created", new Date())
      .withLabel(
        "org.opencontainers.image.source",
        "https://github.com/alpinelinux/docker-alpine",
      )
      .withLabel("org.opencontainers.image.licenses", "MIT")
      .publish("ttl.sh/hello-dagger")

    return ref
  }
}
