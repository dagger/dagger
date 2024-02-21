import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class MyModule {

  @func()
  build(source: Directory, architecture: string, os: string): Container {

    let dir = dag.container()
      .from("golang:1.21")
      .withMountedDirectory("/src", source)
      .withWorkdir("/src")
      .withEnvVariable("GOARCH", architecture)
      .withEnvVariable("GOOS", os)
      .withEnvVariable("CGO_ENABLED", "0")
      .withExec(["go", "build", "-o", "build/"])
      .directory("/src/build")

    return dag.container()
      .from("alpine:latest")
      .withDirectory("/usr/local/bin", dir)
  }

}
