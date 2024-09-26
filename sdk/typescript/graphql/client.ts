import * as opentelemetry from "@opentelemetry/api"
import { GraphQLClient } from "graphql-request"
import fetch from "node-fetch"
import {
  RequestInfo as NodeFetchRequestInfo,
  RequestInit as NodeFetchRequestInit,
} from "node-fetch"

const createFetchWithTimeout =
  (timeout: number) =>
  async (input: URL | RequestInfo, init?: RequestInit): Promise<Response> => {
    if (init?.signal) {
      throw new Error(
        "Internal error: could not create fetch client with timeout",
      )
    }

    const controller = new AbortController()

    const timerId = setTimeout(() => {
      controller.abort()
    }, timeout)

    try {
      return (await fetch(input as NodeFetchRequestInfo, {
        ...(init as NodeFetchRequestInit),
        signal: controller.signal,
      })) as unknown as Response
    } finally {
      clearTimeout(timerId)
    }
  }

/**
 * Customer setter to inject trace parent into the request headers
 * This is required because `graphql-request` 7.0.1 changes its header
 * type to `Headers` which break the default open telemetry propagator.
 */
class CustomSetter {
  set(carrier: Headers, key: string, value: string): void {
    carrier.set(key, value)
  }
}

export function createGQLClient(port: number, token: string): GraphQLClient {
  const client = new GraphQLClient(`http://127.0.0.1:${port}/query`, {
    // 1 week timeout so we should never hit that one.
    // This is to bypass the current graphql-request timeout, which depends on
    // node-fetch and is 5minutes by default.
    fetch: createFetchWithTimeout(1000 * 60 * 60 * 24 * 7),
    headers: {
      Authorization: "Basic " + Buffer.from(token + ":").toString("base64"),
    },
    // Inject trace parent into the request headers so it can be correctly linked
    requestMiddleware: async (req) => {
      opentelemetry.propagation.inject(
        opentelemetry.context.active(),
        req.headers,
        new CustomSetter(),
      )

      return req
    },
  })

  return client
}
