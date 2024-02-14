import { connect, Client, Container } from "@dagger.io/dagger"

connect(
  async (client: Client) => {
    let ctr = client.container().from("alpine")

    // breaks the chain!
    ctr = addMounts(ctr, client)

    const out = await ctr.withExec(["ls"]).stdout()
    console.log(out)
  },
  { LogOutput: process.stderr },
)

function addMounts(ctr: Container, client: Client): Container {
  return ctr
    .withMountedDirectory("/foo", client.host().directory("/tmp/foo"))
    .withMountedDirectory("/bar", client.host().directory("/tmp/bar"))
}
