import * as fs from "fs"
import { GraphQLClient } from "graphql-request"
import * as path from "path"

import { ConnectOpts } from "../connectOpts.js"
import { Bin } from "./bin.js"
import { CLI_VERSION } from "./default.js"

export async function withEngineSession<T>(
  connectOpts: ConnectOpts,
  cb: (gqlClient: GraphQLClient) => Promise<T>,
): Promise<T> {
  const cliBin =
    process.env["_EXPERIMENTAL_DAGGER_CLI_BIN"] ??
    findExecutableOnPath("dagger")
  const engineConn = new Bin(cliBin, CLI_VERSION)
  const gqlClient = await engineConn.Connect(connectOpts)

  try {
    const res = await cb(gqlClient)

    return res
  } finally {
    await engineConn.Close()
  }
}

function findExecutableOnPath(binaryName: string): string | undefined {
  const pathEnv = process.env.PATH
  if (!pathEnv) {
    return undefined
  }

  for (const entry of pathEnv.split(path.delimiter)) {
    if (!entry) {
      continue
    }

    const candidate = path.join(entry, binaryName)
    try {
      fs.accessSync(candidate, fs.constants.X_OK)
      return candidate
    } catch {
      // keep looking
    }
  }

  return undefined
}
