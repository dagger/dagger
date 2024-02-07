import { dag, Container, Directory, object, func, field } from "@dagger.io/dagger"

@object
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class MyModule {

  source: Directory

  // constructor
  constructor (source: Directory) {
    this.source = source
  }

  // publish an image
  @func
  async publish(): Promise<string> {
    return await this.package()
      .publish("ttl.sh/myapp-"+ Math.floor(Math.random() * 10000000))
  }

  // create a production image
  @func
  package(): Container {
    return dag.container().from("nginx:1.25-alpine")
      .withDirectory("/usr/share/nginx/html", this.build())
      .withExposedPort(80)
  }

  // create a production build
  @func
  build(): Directory {
    return dag.node().withContainer(this.buildBaseImage())
      .build()
      .container()
      .directory("./dist")
  }

  // run unit tests
  @func
  async test(): Promise<string> {
    return await dag.node().withContainer(this.buildBaseImage())
      .run(["run", "test:unit", "run"])
      .stdout()
  }

  // build base image
  buildBaseImage(): Container {
    return dag.node()
      .withVersion("21")
      .withNpm()
      .withSource(this.source)
      .install([])
      .container()
  }

}
