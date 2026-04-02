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

  /**
   * Select via node(id:) with an inline fragment on the given type.
   * Produces: node(id: "...") { ... on TypeName { children } }
   */
  selectNode(id: string, typeName: string): Context {
    return new Context(
      [...this._queryTree, { operation: "node", args: { id }, inlineType: typeName }],
      this._connection,
    )
  }

  execute<T>(): Promise<T> {
    return computeQuery(this._queryTree, this._connection.getGQLClient())
  }
}
