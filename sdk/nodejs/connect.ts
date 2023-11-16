import { Writable } from "node:stream"

import { Client } from "./api/client.gen.js"
import { Context, defaultContext } from "./context/context.js"
import { createGQLClient } from "./graphql/client.js"
import { Bin, CLI_VERSION } from "./provisioning/index.js"

/**
 * ConnectOpts defines option used to connect to an engine.
 */
export interface ConnectOpts {
  /**
   * Use to overwrite Dagger workdir
   * @defaultValue process.cwd()
   */
  Workdir?: string
  /**
   * Enable logs output
   * @example
   * LogOutput
   * ```ts
   * connect(async (client: Client) => {
    const source = await client.host().workdir().id()
    ...
    }, {LogOutput: process.stdout})
    ```
   */
  LogOutput?: Writable
}

export type CallbackFct = (client: Client) => Promise<void>

/**
 * Close global client connection
 */
export function close() {
  defaultContext.close()
}

/**
 * connect runs GraphQL server and initializes a
 * GraphQL client to execute query on it through its callback.
 * This implementation is based on the existing Go SDK.
 */
export async function connect(
  cb: CallbackFct,
  config: ConnectOpts = {}
): Promise<void> {
  let client: Client
  let close: null | (() => void) = null

  // Prefer DAGGER_SESSION_PORT if set
  const daggerSessionPort = process.env["DAGGER_SESSION_PORT"]
  if (daggerSessionPort) {
    const sessionToken = process.env["DAGGER_SESSION_TOKEN"]
    if (!sessionToken) {
      throw new Error(
        "DAGGER_SESSION_TOKEN must be set when using DAGGER_SESSION_PORT"
      )
    }

    if (config.Workdir && config.Workdir !== "") {
      throw new Error(
        "cannot configure workdir for existing session (please use --workdir or host.directory with absolute paths instead)"
      )
    }

    client = new Client({
      ctx: new Context({
        client: createGQLClient(Number(daggerSessionPort), sessionToken),
      }),
    })
  } else {
    // Otherwise, prefer _EXPERIMENTAL_DAGGER_CLI_BIN, with fallback behavior of
    // downloading the CLI and using that as the bin.
    const cliBin = process.env["_EXPERIMENTAL_DAGGER_CLI_BIN"]
    const engineConn = new Bin(cliBin, CLI_VERSION)
    const gqlClient = await engineConn.Connect(config)
    close = () => engineConn.Close()

    client = new Client({
      ctx: new Context({
        client: gqlClient,
        subProcess: engineConn.subProcess,
      }),
    })
  }

  // Warning shall be throw if versions are not compatible
  try {
    await client.checkVersionCompatibility(CLI_VERSION)
  } catch (e) {
    console.error("failed to check version compatibility:", e)
  }

  await cb(client).finally(async () => {
    if (close) {
      close()
    }
  })
}
