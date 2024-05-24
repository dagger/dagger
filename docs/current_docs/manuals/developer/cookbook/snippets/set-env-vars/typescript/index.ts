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
        envVariables({
          ENV_VAR_1: "VALUE 1",
          ENV_VAR_2: "VALUE 2",
          ENV_VAR_3: "VALUE 3",
        }),
      )
      .withExec(["env"])
      .stdout()
  }
}

function envVariables(envs: Record<string, string>) {
  return (c: Container): Container => {
    Object.entries(envs).forEach(([key, value]) => {
      c = c.withEnvVariable(key, value)
    })
    return c
  }
}
