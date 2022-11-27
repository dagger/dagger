import { connect } from "@dagger.io/dagger"

// initialize Dagger client
connect(async (client) => {
  // get Node image
  // get Node version
  let node = client.container().from("node:16").withExec(["node", "-v"])

  // execute
  let version = await node.stdout()

  // print output
  console.log("Hello from Dagger and Node " + version)
})
