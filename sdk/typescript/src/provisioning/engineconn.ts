import { GraphQLClient } from "graphql-request"
import { Writable } from "node:stream"

export interface ConnectOpts {
  Workdir?: string
  Project?: string
  LogOutput?: Writable
  Timeout?: number
}

export interface ConnectParams {
  port: number
  session_token: string
}

export interface EngineConn {
  /**
   * Library connection provisioning, it returns a ready to use GraphQL client
   * connected to the Dagger engine.
   *
   * This test multiple options to connect to the Dagger Engine.
   * 1. Check for already running engine through `DAGGER_SESSION_PORT` & `DAGGER_SESSION_TOKEN`
   * environment variable.
   * 2. Auto provision the engine from the Dagger CLI (install it if it doesn't exist) and
   * connect the client.
   */
  Connect: (opts: ConnectOpts) => Promise<GraphQLClient>

  /**
   * Close stops the current connection.
   */
  Close: () => void
}
