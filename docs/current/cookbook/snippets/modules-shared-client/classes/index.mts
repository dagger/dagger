import { connect, Client } from "@dagger.io/dagger"

import { Pipelines } from "./pipelines.mts"

connect(
  // initialize Dagger client
  // pass client to method imported from another module
  async (client: Client) => {
    // create pipeline object passing the client
    const pipeline = new Pipelines(client)

    // call pipeline method
    console.log(await pipeline.version())
  },
  { LogOutput: process.stderr }
)
