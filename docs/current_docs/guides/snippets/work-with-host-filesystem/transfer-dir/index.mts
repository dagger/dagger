import { connect, Client } from "@dagger.io/dagger"

connect(
  async (client: Client) => {
    const contents = await client
      .container()
      .from("alpine:latest")
      .withDirectory("/host", client.host().directory("."))
      .withExec(["ls", "/host"])
      .stdout()

    console.log(contents)
  },
  { LogOutput: process.stderr },
)
