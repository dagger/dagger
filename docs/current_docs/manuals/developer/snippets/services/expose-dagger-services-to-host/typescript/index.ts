import { dag, object, func, Service } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Start and return an HTTP service
   */
  @func()
  httpService(): Service {
    return dag
      .container()
      .from("python")
      .withWorkdir("/srv")
      .withNewFile("index.html", "Hello, world!")
      .withExec(["python", "-m", "http.server", "8080"])
      .withExposedPort(8080)
      .asService()
  }
}
