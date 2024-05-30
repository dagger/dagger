import { dag, Container, Directory, ClientContainerOpts, object, func, ContainerPublishOpts } from "@dagger.io/dagger"

@object()
class Ts {
  @func()
  async build(src: Directory): Promise<string> {
    // platforms to build for and push in a multi-platform image
    const platforms = [
        "linux/amd64)", // a.k.a. x86_64
        "linux/arm64", // a.k.a. aarch64
        "linux/s390x", // a.k.a. IBM S/390 
    ]

    // container registry for multi-platform image
    const imageRepo = "ttl.sh/myapp:latest"
    
    let platformVariants: Array<Container> = []
    for (const platform of platforms) {
      const ctr = dag
        .container({platform: platform} as ClientContainerOpts)
        .from("golang:1.20-alpoine")
        // mount source 
        .withDirectory("/src", src)
        // mount empty dir where built binary will live 
        .withDirectory("/output", new Directory())
        // ensure binary will be statically linked and thus executable
        // in the final image 
        .withEnvVariable("CGO_ENABLED", "0")
        .withWorkdir("/src")
        .withExec(["go", "build", "-o", "/output/hello"])
  
      // select output directory
      const outputDir = ctr.directory("/output")

      // wrap output directory in a new empty container marked 
      // with the same platform
      const binaryCtr = dag
        .container({platform: platform} as ClientContainerOpts)
        .withRootfs(outputDir)

      platformVariants.push(binaryCtr)
    }
    // publish to registry 
    const imageDigest = dag
      .container()
      .publish(imageRepo, {platformVariants:platformVariants} as ContainerPublishOpts)

    return await imageDigest
  }
}
