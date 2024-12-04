import { GraphQLClient } from "graphql-request"

export class Connection {
  constructor(private _gqlClient?: GraphQLClient) {}

  resetClient() {
    this._gqlClient = undefined
  }

  setGQLClient(gqlClient: GraphQLClient) {
    this._gqlClient = gqlClient
  }

  getGQLClient(): GraphQLClient {
    if (!this._gqlClient) {
      throw new Error("GraphQL client is not set")
    }

    return this._gqlClient
  }
}

export const globalConnection = new Connection()
