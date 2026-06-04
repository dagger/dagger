import { dag, object, Directory, File, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  build(src: Directory, arch: string, os: string): File {
    return dag
      .container()
      .from("golang:1.21")
      .withMountedDirectory("/src", src)
      .withWorkdir("/src")
      .withEnvVariable("GOARCH", arch)
      .withEnvVariable("GOOS", os)
      .withEnvVariable("CGO_ENABLED", "0")
      .withExec(["go", "build", "-o", "build/"])
      .file("/src/build/hello")
  }
}
