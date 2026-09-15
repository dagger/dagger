import { dag, object, Directory, Container, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  build(src: Directory, arch: string, os: string): Container {
    return dag
      .container()
      .from("golang:1.21")
      .withMountedDirectory("/src", src)
      .withWorkdir("/src")
      .withEnvVariable("GOARCH", arch)
      .withEnvVariable("GOOS", os)
      .withEnvVariable("CGO_ENABLED", "0")
      .withExec(["go", "build", "-o", "build/"])
  }
}
