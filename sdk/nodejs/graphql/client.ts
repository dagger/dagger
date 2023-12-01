import { GraphQLClient } from "graphql-request"

export function createGQLClient(port: number, token: string): GraphQLClient {
  return new GraphQLClient(`http://127.0.0.1:${port}/query`, {
    headers: {
      Authorization: "Basic " + Buffer.from(token + ":").toString("base64"),
    },
  })
}
