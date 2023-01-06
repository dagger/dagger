import { Writable } from "node:stream"

import Client from "./api/client.gen.js"
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
   * Use to overwrite Dagger config
   * @defaultValue dagger.json
   */
  ConfigPath?: string
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

export interface ConnectParams {
  port: number
  session_token: string
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
  // Create config with default values that may be overridden
  // by config if values are set.
  const _config: ConnectOpts = {
    Workdir: process.env["DAGGER_WORKDIR"] || process.cwd(),
    ConfigPath: process.env["DAGGER_CONFIG"] || "./dagger.json",
    ...config,
  }

  let client
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
    client = new Client({
      host: `http://127.0.0.1:${daggerSessionPort}/query`,
      sessionToken: sessionToken,
    })
  } else {
    // Otherwise, prefer _EXPERIMENTAL_DAGGER_CLI_BIN, with fallback behavior of
    // downloading the CLI and using that as the bin.
    const cliBin = process.env["_EXPERIMENTAL_DAGGER_CLI_BIN"]
    const engineConn = new Bin(cliBin, CLI_VERSION)
    client = await engineConn.Connect(_config)
    close = () => engineConn.Close()
  }

  await cb(client).finally(async () => {
    if (close) {
      close()
    }
  })
}
