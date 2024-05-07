import * as opentelemetry from "@opentelemetry/api"
import { GraphQLClient } from "graphql-request"

export function createGQLClient(port: number, token: string): GraphQLClient {
  const client = new GraphQLClient(`http://127.0.0.1:${port}/query`, {
    headers: {
      Authorization: "Basic " + Buffer.from(token + ":").toString("base64"),
    },
    // Inject trace parent into the request headers so it can be correctly linked
    requestMiddleware: async (req) => {
      opentelemetry.propagation.inject(
        opentelemetry.context.active(),
        req.headers,
      )

      return req
    },
  })

  return client
}
