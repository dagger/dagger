import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Build and return directory of go binaries
   */
  @func()
  build(src: Directory): Directory {
    // define build matrix
    const gooses = ["linux", "darwin"]
    const goarches = ["amd64", "arm64"]

    // create empty directory to put build artifacts
    let outputs = dag.directory()

    const golang = dag
      .container()
      .from("golang:latest")
      .withDirectory("/src", src)
      .withWorkdir("/src")

    for (const goos of gooses) {
      for (const goarch of goarches) {
        // create a directory for each OS and architecture
        const path = `build/${goos}/${goarch}/`

        // build artifact
        const build = golang
          .withEnvVariable("GOOS", goos)
          .withEnvVariable("GOARCH", goarch)
          .withExec(["go", "build", "-o", path])

        // add build to outputs
        outputs = outputs.withDirectory(path, build.directory(path))
      }
    }

    return outputs
  }
}
