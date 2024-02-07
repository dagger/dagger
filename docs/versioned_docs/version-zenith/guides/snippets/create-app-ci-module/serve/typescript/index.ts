import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class MyModule {

  @func
  serve(): Service {
    return this.package().asService()
  }

  @func
  async publish(): Promise<string>{
    return this.package()
      .publish("ttl.sh/myapp-"+ Math.floor(Math.random() * 10000000))
  }

  @func
  package(): Container {
    return dag.container().from("nginx:1.25-alpine")
      .withDirectory("/usr/share/nginx/html", this.build())
      .withExposedPort(80)
  }

  @func
  build(): Directory {
    return this.buildBaseImage()
      .build()
      .container()
      .directory("./dist")
  }

  @func
  async test(): Promise<string> {
    return this.buildBaseImage()
      .run(["run", "test:unit", "run"])
      .stdout()
  }

  buildBaseImage(): Node {
    return dag.node()
      .withVersion("21")
      .withNpm()
      .withSource(dag.currentModule().source())
      .install(false)
  }

}
