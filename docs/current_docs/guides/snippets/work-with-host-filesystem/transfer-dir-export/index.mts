import { connect, Client } from "@dagger.io/dagger"

connect(
  async (client: Client) => {
    const contents = await client
      .container()
      .from("alpine:latest")
      .withDirectory("/host", client.host().directory("/tmp/sandbox"))
      .withExec(["/bin/sh", "-c", `echo foo > /host/bar`])
      .directory("/host")
      .export("/tmp/sandbox")

    console.log(contents)
  },
  { LogOutput: process.stderr },
)
