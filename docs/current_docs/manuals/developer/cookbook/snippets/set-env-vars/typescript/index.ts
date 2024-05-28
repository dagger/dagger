import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Set environment variables in a container
   */
  @func()
  async setEnv(): Promise<string> {
    return await dag
      .container()
      .from("alpine")
      .with(
        envVariables([
          ["ENV_VAR_1", "VALUE 1"],
          ["ENV_VAR_2", "VALUE 2"],
          ["ENV_VAR_3", "VALUE_3"],
        ]),
      )
      .withExec(["env"])
      .stdout()
  }
}

function envVariables(envs: Array<[string, string]>) {
  return (c: Container): Container => {
    for (const [key, value] of envs) {
      c = c.withEnvVariable(key, value)
    }
    return c
  }
}
