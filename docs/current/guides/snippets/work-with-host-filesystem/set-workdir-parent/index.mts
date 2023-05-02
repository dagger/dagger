import Client, { connect } from "@dagger.io/dagger"

connect(async (client: Client) => {
  
  console.log("foo")

}, {LogOutput: process.stderr, Workdir: ".."})
