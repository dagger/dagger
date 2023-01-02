import { Writable } from "node:stream"

import Client from "../api/client.gen.js"

export interface ConnectOpts {
  Workdir?: string
  Project?: string
  LogOutput?: Writable
  Timeout?: number
}

export interface EngineConn {
  /**
   * Addr returns the connector address.
   */
  Addr: () => string

  /**
   * Connect initializes a ready to use GraphQL Client that
   * points to the engine.
   */
  Connect: (opts: ConnectOpts) => Promise<Client>

  /**
   * Close stops the current connection.
   */
  Close: () => Promise<void>
}
