import { connect, Client, Container } from "@dagger.io/dagger"

connect(
  async (client: Client) => {
    const out = await client
      .container()
      .from("alpine")
      .with(mounts(client))
      .withExec(["ls"])
      .stdout()
    console.log(out)
  },
  { LogOutput: process.stderr },
)

function mounts(client: Client) {
  return (ctr: Container): Container =>
    ctr
      .withMountedDirectory("/foo", client.host().directory("/tmp/foo"))
      .withMountedDirectory("/bar", client.host().directory("/tmp/bar"))
}
