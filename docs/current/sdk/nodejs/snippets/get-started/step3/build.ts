import Client, { connect } from "@dagger.io/dagger"

// initialize Dagger client
connect(async (client: Client) => {
  // get Node image
  // get Node version
  const node = await client.container().from("node:16").exec(["node", "-v"])

  // execute
  const version = await node.stdout()

  // print output
  console.log("Hello from Dagger and Node " + version.contents)
})
