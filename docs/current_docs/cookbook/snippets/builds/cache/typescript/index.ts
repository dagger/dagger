import { dag, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Run a build with cache invalidation
   */
  @func()
  async build(): Promise<string> {
    const ref = await dag
      .container()
      .from("alpine")
      // comment out the line below to see the cached date output
      .withEnvVariable("CACHEBUSTER", Date.now().toString())
      .withExec(["date"])
      .stdout()

    return ref
  }
}
