import {
  dag,
  Container,
  Directory,
  Service,
  object,
  func,
} from "@dagger.io/dagger";

@object()
class MyModule {
  // create a service from the production image
  @func()
  serve(source: Directory): Service {
    return this.package(source).asService();
  }

  // publish an image
  @func()
  async publish(source: Directory): Promise<string> {
    return await this.package(source).publish(
      "ttl.sh/myapp-" + Math.floor(Math.random() * 10000000),
    );
  }

  // create a production image
  @func()
  package(source: Directory): Container {
    return dag
      .container()
      .from("nginx:1.25-alpine")
      .withDirectory("/usr/share/nginx/html", this.build(source))
      .withExposedPort(80);
  }

  // create a production build
  @func()
  build(source: Directory): Directory {
    return dag
      .node()
      .withContainer(this.buildBaseImage(source))
      .build()
      .container()
      .directory("./dist");
  }

  // run unit tests
  @func()
  async test(source: Directory): Promise<string> {
    return await dag
      .node()
      .withContainer(this.buildBaseImage(source))
      .run(["run", "test:unit", "run"])
      .stdout();
  }

  // build base image
  buildBaseImage(source: Directory): Container {
    return dag
      .node()
      .withVersion("21")
      .withNpm()
      .withSource(source)
      .install([])
      .container();
  }
}
