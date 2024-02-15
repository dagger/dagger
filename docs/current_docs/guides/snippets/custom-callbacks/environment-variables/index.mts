import { connect, Client, Container } from "@dagger.io/dagger"

// create Dagger client
connect(
  async (client: Client) => {
    // setup container and
    // define environment variables
    const ctr = client
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

    // print environment variables
    console.log(await ctr.stdout())
  },
  { LogOutput: process.stderr },
)

function envVariables(envs: Record<string, string>) {
  return (c: Container): Container => {
    Object.entries(envs).forEach(([key, value]) => {
      c = c.withEnvVariable(key, value)
    })
    return c
  }
}
