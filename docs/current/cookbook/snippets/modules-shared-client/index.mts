import { connect, Client } from "@dagger.io/dagger"

import * as pipelines from "./pipelines.mts"

connect(
  // initialize Dagger client
  // pass client to method imported from another module
  async (client: Client) => {
    console.log(await pipelines.version(client))
  },
  { LogOutput: process.stderr }
)
