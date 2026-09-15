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
      [
        ...this._queryTree,
        { operation: "node", args: { id }, inlineType: typeName },
      ],
      this._connection,
    )
  }

  execute<T>(): Promise<T> {
    return computeQuery(this._queryTree, this._connection.getGQLClient())
  }
}

/**
 * Common base class for every generated API class (Client, Container, and
 * dependency-contributed types).
 *
 * It lives here in the SDK runtime rather than in the generated client.gen.ts
 * so that per-dependency generated files (e.g. hello.gen.ts) can `extends
 * BaseClient` without importing a value from client.gen.ts — client.gen.ts
 * `export *`s those dep files, so a value import would create an ESM cycle.
 * client.gen.ts re-exports BaseClient to keep `import { BaseClient } from
 * "./client.gen.js"` working for existing consumers.
 */
export class BaseClient {
  /**
   * @hidden
   */
  constructor(protected _ctx: Context = new Context()) {}
}
