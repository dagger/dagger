import { dag, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Set a single environment variable in a container
   */
  @func()
  async setEnvVar(): Promise<string> {
    return await dag
      .container()
      .from("alpine")
      .withEnvVariable("ENV_VAR", "VALUE")
      .withExec(["env"])
      .stdout()
  }
}
