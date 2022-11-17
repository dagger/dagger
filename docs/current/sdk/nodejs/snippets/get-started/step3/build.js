import { connect } from "@dagger.io/dagger"

 // initialize Dagger client
connect(async (client) => {

  // get Node image
  // get Node version
  let node = await client
    .container()
    .from("node:16")
    .exec(["node", "-v"])

  // execute
  let version = await node
    .stdout()
    .contents()

  // print output
  console.log("Hello from Dagger and Node " + version.contents)
});
