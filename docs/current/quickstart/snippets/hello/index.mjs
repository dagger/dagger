import { connect } from "@dagger.io/dagger"

// initialize Dagger client
connect(async (client) => {

  // use a node:16-slim container
  // get version
  const node = client.container().from("node:16-slim").withExec(["node", "-v"])

  // execute
  const version = await node.stdout()

  // print output
  console.log("Hello from Dagger and Node " + version)
}, { LogOutput: process.stdout })
