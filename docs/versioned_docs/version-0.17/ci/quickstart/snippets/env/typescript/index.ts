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
   * Build a ready-to-use development environment
   */
  @func()
  buildEnv(@argument({ defaultPath: "/" }) source: Directory): Container {
    // create a Dagger cache volume for dependencies
    const nodeCache = dag.cacheVolume("node")
    return (
      dag
        .container()
        // start from a base Node.js container
        .from("node:21-slim")
        // add the source code at /src
        .withDirectory("/src", source)
        // mount the cache volume at /root/.npm
        .withMountedCache("/root/.npm", nodeCache)
        // change the working directory to /src
        .withWorkdir("/src")
        // run npm install to install dependencies
        .withExec(["npm", "install"])
    )
  }
}
