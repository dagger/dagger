import { GraphQLClient } from "graphql-request"

import { ConnectOpts } from "../../connectOpts.js"
import { EngineConn, loadProvioningLibrary } from "../../provisioning/index.js"

interface ContextConfig {
  client?: GraphQLClient
  engineConn?: EngineConn
}

/**
 * Context abstracts the connection to the engine.
 *
 * It's required to implement the default global SDK.
 * Its purpose is to store and returns the connection to the graphQL API, if
 * no connection is set, it can create its own.
 *
 * This is also useful for lazy evaluation with the default global client,
 * this one should only run the engine if it actually executes something.
 */
export class Context {
  private _client?: GraphQLClient
  private _engineConn?: EngineConn

  constructor(config?: ContextConfig) {
    this._client = config?.client
    this._engineConn = config?.engineConn
  }

  /**
   * Returns a GraphQL client connected to the engine.
   *
   * If no client is set, it will create one.
   */
  public async connection(cfg: ConnectOpts = {}): Promise<GraphQLClient> {
    if (!this._client) {
      const engineConn = await loadProvioningLibrary()

      const client = await engineConn.Connect(cfg)

      this._client = client
      this._engineConn = engineConn
    }

    return this._client
  }

  public getGQLClient(): GraphQLClient {
    if (!this._client) {
      throw new Error(
        "graphQL connection not established yet, please use it inside a connect or connection function.",
      )
    }

    return this._client
  }

  /**
   * Close the connection and the engine if this one was started by the node
   * SDK.
   */
  public close(): void {
    if (this._engineConn) {
      this._engineConn.Close()
    }

    // Reset client, so it can restart a new connection if necessary
    this._client = undefined
  }
}

/**
 * Expose a default context for the global client
 */
export const defaultContext = new Context()
