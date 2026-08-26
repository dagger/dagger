import {
  dag,
  Container,
  Directory,
  object,
  func,
  argument,
} from "@dagger.io/dagger"

@object()
class HelloDagger {
  /**
   * Build the application container
   */
  @func()
  build(@argument({ defaultPath: "/" }) source: Directory): Container {
    // get the build environment container
    // by calling another Dagger Function
    const build = this.buildEnv(source)
      // build the application
      .withExec(["npm", "run", "build"])
      // get the build output directory
      .directory("./dist")
    return (
      dag
        .container()
        // start from a slim NGINX container
        .from("nginx:1.25-alpine")
        // copy the build output directory to the container
        .withDirectory("/usr/share/nginx/html", build)
        // expose the container port
        .withExposedPort(80)
    )
  }
}
