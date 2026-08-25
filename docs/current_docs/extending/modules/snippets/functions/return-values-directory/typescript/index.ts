import { dag, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  goBuilder(src: Directory, arch: string, os: string): Directory {
    return dag
      .container()
      .from("golang:1.21")
      .withMountedDirectory("/src", src)
      .withWorkdir("/src")
      .withEnvVariable("GOARCH", arch)
      .withEnvVariable("GOOS", os)
      .withEnvVariable("CGO_ENABLED", "0")
      .withExec(["go", "build", "-o", "build/"])
      .directory("/src/build")
  }
}
