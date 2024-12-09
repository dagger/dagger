import { GraphQLClient } from "graphql-request"

import { ConnectOpts } from "../connectOpts.js"
import { Bin } from "./bin.js"
import { CLI_VERSION } from "./default.js"

export async function withEngineSession<T>(
  connectOpts: ConnectOpts,
  cb: (gqlClient: GraphQLClient) => Promise<T>,
): Promise<T> {
  const cliBin = process.env["_EXPERIMENTAL_DAGGER_CLI_BIN"]
  const engineConn = new Bin(cliBin, CLI_VERSION)
  const gqlClient = await engineConn.Connect(connectOpts)

  try {
    const res = await cb(gqlClient)

    return res
  } finally {
    await engineConn.Close()
  }
}
