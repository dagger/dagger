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
      .withNewFile("index.html", { contents: "Hello, world!" })
      .withExec(["python", "-m", "http.server", "8080"])
      .withExposedPort(8080)
      .asService()
  }

  /**
   * Send a request to an HTTP service and return the response
   */
  @func()
  async get(): Promise<string> {
    return await dag
      .container()
      .from("alpine")
      .withServiceBinding("www", this.httpService())
      .withExec(["wget", "-O-", "http://www:8080"])
      .stdout()
  }
}
