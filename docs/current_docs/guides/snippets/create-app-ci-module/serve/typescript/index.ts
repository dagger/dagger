import { dag, Container, Directory, object, func, Service } from "@dagger.io/dagger"

@object()
class MyModule {
  /*
   * Create a service from the production image
   */
  @func()
  serve(source: Directory): Service {
    return this.package(source).asService()
  }

  /*
   * Publish an image
   */
  @func()
  async publish(source: Directory): Promise<string> {
    return await this.package(source).publish(
      "ttl.sh/myapp-" + Math.floor(Math.random() * 10000000),
    )
  }

  /*
   * Create a production image
   */
  @func()
  package(source: Directory): Container {
    return dag
      .container()
      .from("nginx:1.25-alpine")
      .withDirectory("/usr/share/nginx/html", this.build(source))
      .withExposedPort(80)
  }

  /*
   * Create a production build
   */
  @func()
  build(source: Directory): Directory {
    return dag
      .node({ ctr: this.buildBaseImage(source) })
      .commands()
      .build()
      .directory("./dist")
  }

  /*
   * Run unit tests
   */
  @func()
  async test(source: Directory): Promise<string> {
    return await dag
      .node({ ctr: this.buildBaseImage(source) })
      .commands()
      .run(["test:unit", "run"])
      .stdout()
  }

  /*
   * Build base image
   */
   buildBaseImage(source: Directory): Container {
    return dag
      .node({ version: "21" })
      .withNpm()
      .withSource(source)
      .install([])
      .container()
  }
}
