import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class HelloDagger {
  /**
   * Tests, builds and publishes the application
   */
  @func()
  async publish(source: Directory): Promise<string> {
    this.test(source)
    return await this.build(source).publish(
      "ttl.sh/myapp-" + Math.floor(Math.random() * 10000000),
    )
  }

  /**
   * Returns a container with the production build and an NGINX service
   */
  @func()
  build(source: Directory): Container {
    const build = this.buildEnv(source)
      .withExec(["npm", "run", "build"])
      .directory("./dist")
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
    return this.buildEnv(source)
      .withExec(["npm", "run", "test:unit", "run"])
      .stdout()
  }

  /**
   * Returns a container with the build environment
   */
  @func()
  buildEnv(source: Directory): Container {
    const nodeCache = dag.cacheVolume("node")
    return dag
      .container()
      .from("node:21-slim")
      .withDirectory("/src", source)
      .withMountedCache("/src/node_modules", nodeCache)
      .withWorkdir("/src")
      .withExec(["npm", "install"])
  }
}
