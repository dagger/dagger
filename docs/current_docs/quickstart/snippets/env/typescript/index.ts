import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class HelloDagger {
  /**
   * Returns a container with the build environment
   */
  @func()
  buildEnv(source: Directory): Container {
    // create a Dagger cache volume for dependencies
    const nodeCache = dag.cacheVolume("node")
    return dag
      .container()
      // start from a base Node.js container
      .from("node:21-slim")
      // add the source code at /src
      .withDirectory("/src", source)
      // mount the cache volume at /src/node_modules
      .withMountedCache("/src/node_modules", nodeCache)
      // change the working directory to /src
      .withWorkdir("/src")
      // run npm install to install dependencies
      .withExec(["npm", "install"])
  }
}
