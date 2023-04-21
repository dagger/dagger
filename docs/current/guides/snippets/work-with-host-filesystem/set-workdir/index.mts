import Client, { connect } from "@dagger.io/dagger"

connect(async (client: Client) => {
  
}, {LogOutput: process.stderr, Workdir: "."})
