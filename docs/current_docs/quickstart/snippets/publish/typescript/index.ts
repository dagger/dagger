import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class HelloDagger {
  /**
   * Tests, builds and publishes the application
   */
  @func()
  async publish(source: Directory): Promise<string> {
    // run unit tests
    this.test(source)
    // build and publish the container
    return await this.build(source).publish("ttl.sh/myapp-" + Math.floor(Math.random() * 10000000))
  }

  /**
   * Returns a container with the production build and an NGINX service
   */
  @func()
  build(source: Directory): Container {
    // perform a multi-stage build
    // stage 1
    // use the build environment container
    // build the application
    // return the build output directory
    const build = this.buildEnv(source).withExec(["npm", "run", "build"]).directory("./dist")
    // stage 2
    // start from a base nginx container
    // copy the build output directory to it
    // expose container port 8080
    return dag
      .container()
      .from("nginx:1.25-alpine")
      .withDirectory("/usr/share/nginx/html", build)
      .withExposedPort(8080)
  }

  /**
   * Returns the result of running unit tests
   */
  @func()
  async test(source: Directory): Promise<string> {
    // use the build environment container
    // run unit tests
    return this.buildEnv(source).withExec(["npm", "run", "test:unit", "run"]).stdout()
  }

  /**
   * Returns a container with the build environment
   */
  @func()
  buildEnv(source: Directory): Container {
    // create a Dagger cache volume for dependencies
    const nodeCache = dag.cacheVolume("node")
    // create the build environment
    // start from a base node container
    // add source code
    // install dependencies
    return dag
      .container()
      .from("node:21-slim")
      .withDirectory("/src", source.withoutDirectory("dagger"))
      .withMountedCache("/src/node_modules", nodeCache)
      .withWorkdir("/src")
      .withExec(["npm", "install"])
  }
}
