import {
  dag,
  Container,
  Directory,
  Platform,
  object,
  func,
} from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Build and publish multi-platform image
   * @param src source code location
   */
  @func()
  async build(src: Directory): Promise<string> {
    // platforms to build for and push in a multi-platform image
    const platforms: Platform[] = [
      "linux/amd64" as Platform, // a.k.a. x86_64
      "linux/arm64" as Platform, // a.k.a. aarch64
      "linux/s390x" as Platform, // a.k.a. IBM S/390
    ]

    // container registry for multi-platform image
    const imageRepo = "ttl.sh/myapp:latest"
    const platformVariants: Array<Container> = []
    for (const platform of platforms) {
      const ctr = dag
        .container({ platform: platform })
        .from("golang:1.21-alpine")
        // mount source
        .withDirectory("/src", src)
        // mount empty dir where built binary will live
        .withDirectory("/output", dag.directory())
        // ensure binary will be statically linked and thus executable
        // in the final image
        .withEnvVariable("CGO_ENABLED", "0")
        .withWorkdir("/src")
        .withExec(["go", "build", "-o", "/output/hello"])

      // select output directory
      const outputDir = ctr.directory("/output")

      // wrap output directory in a new empty container marked
      // with the same platform
      const binaryCtr = await dag
        .container({ platform: platform })
        .withRootfs(outputDir)

      platformVariants.push(binaryCtr)
    }
    // publish to registry
    const imageDigest = await dag
      .container()
      .publish(imageRepo, { platformVariants: platformVariants })

    return imageDigest
  }
}
