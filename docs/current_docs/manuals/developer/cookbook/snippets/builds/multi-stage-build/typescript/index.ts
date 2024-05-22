import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Build multi stage docker container and publish to registry
   */
  @func()
  build(dir: Directory): Promise<string> {
    // build app
    const builder = dag.container()
      .from("golang:latest")
      .withDirectory("/src", dir)
      .withWorkdir("/src")
      .withEnvVariable("CGO_ENABLED", "0")
      .withExec(["go", "build", "-o", "myapp"])

    // publish binary on alpine base
    const prodImage = dag.container()
      .from("alpine")
      .withFile("/bin/myapp", builder.file("/src/myapp"))
      .withEntrypoint(["/bin/myapp"])

    // publish to ttl.sh registry
    const addr = prodImage.publish("ttl.sh/myapp:latest")

    return addr
  }
}
