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
      .withExposedPort(8080)
      .asService({ args: ["python", "-m", "http.server", "8080"] })
  }

  /**
   * Send a request to an HTTP service accessible on localhost
   */
  @func()
  async get(): Promise<string> {
    return await dag
      .container()
      .from("alpine")
      .withLocalhostForward(8080, this.httpService())
      .withExec(["wget", "-O-", "http://127.0.0.1:8080"])
      .stdout()
  }
}
