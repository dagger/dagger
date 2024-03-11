import { dag, Container, Directory, object, func, Service } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Starts and returns an HTTP service
   */
  @func()
  httpService(): Service {
    return dag.container()
      .from("python")
      .withDirectory("/srv", dag.directory().withNewFile("index.html", "Hello, world!"))
      .withWorkdir("/srv")
      .withExec(["python", "-m", "http.server", "8080"])
      .withExposedPort(8080)
      .asService()
  }

  /**
   * Sends a request to an HTTP service and returns the response
   */
  @func()
  async get(): Promise<string> {
    return await dag.container()
      .from("alpine")
      .withServiceBinding("www", this.httpService())
      .withExec(["wget", "-O-", "http://www:8080"])
      .stdout()
  }

}
