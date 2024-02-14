import { connect, Client } from "@dagger.io/dagger"

import * as alpine from "./alpine.mts"

connect(
  // initialize Dagger client
  // pass client to method imported from another module
  async (client: Client) => {
    console.log(await alpine.version(client))
  },
  { LogOutput: process.stderr },
)
