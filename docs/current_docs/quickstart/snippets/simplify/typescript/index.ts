import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class HelloDagger {
  /**
   * Publish the application container after building and testing it on-the-fly
   */
  @func()
  async publish(source: Directory): Promise<string> {
    this.test(source)
    return await this.build(source).publish(
      "ttl.sh/hello-dagger-" + Math.floor(Math.random() * 10000000),
    )
  }

  /**
   * Build the application container
   */
  @func()
  build(source: Directory): Container {
    const build = dag
      .node({ ctr: this.buildEnv(source) })
      .commands()
      .run(["build"])
      .directory("./dist")
    return dag
      .container()
      .from("nginx:1.25-alpine")
      .withDirectory("/usr/share/nginx/html", build)
      .withExposedPort(80)
  }

  /**
   * Return the result of running unit tests
   */
  @func()
  async test(source: Directory): Promise<string> {
    return await dag
      .node({ ctr: this.buildEnv(source) })
      .commands()
      .run(["test:unit", "run"])
      .stdout()
  }

  /**
   * Build a ready-to-use development environment
   */
  @func()
  buildEnv(source: Directory): Container {
    return dag
      .node({ version: "21" })
      .withNpm()
      .withSource(source)
      .install([])
      .container()
  }
}
