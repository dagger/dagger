import { GraphQLClient } from "graphql-request"

import { computeQuery, QueryTree } from "./graphql/compute_query.js"
import { globalConnection } from "./graphql/connection.js"

export class Context {
  constructor(
    private _queryTree: QueryTree[] = [],
    private _connection = globalConnection,
  ) {}

  getGQLClient(): GraphQLClient {
    return this._connection.getGQLClient()
  }

  copy(): Context {
    return new Context([], this._connection)
  }

  select(operation: string, args?: Record<string, unknown>): Context {
    return new Context(
      [...this._queryTree, { operation, args }],
      this._connection,
    )
  }

  execute<T>(): Promise<T> {
    return computeQuery(this._queryTree, this._connection.getGQLClient())
  }
}

/**
 * Common base class for every generated API class (Client, Container, and
 * dep-contributed types). Lives here rather than in the generated
 * client.gen.ts so per-dep generated files can extend it without creating
 * an ESM cycle (client.gen.ts → <dep>.gen.ts → client.gen.ts).
 */
export class BaseClient {
  /**
   * @hidden
   */
  constructor(protected _ctx: Context = new Context()) {}
}
