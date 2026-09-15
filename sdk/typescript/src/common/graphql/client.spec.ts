import assert from "assert"
import { afterEach, describe, it } from "mocha"

import { isBun } from "../utils.js"
import { createGQLClient } from "./client.js"

type BunRequestInit = RequestInit & {
  timeout?: boolean | number
}

describe("GraphQL client", function () {
  const originalFetch = globalThis.fetch

  afterEach(function () {
    globalThis.fetch = originalFetch
  })

  it("disables the native Bun fetch timeout", async function () {
    if (!isBun()) {
      this.skip()
    }

    let requestInit: BunRequestInit | undefined
    globalThis.fetch = (async (_input, init) => {
      requestInit = init as BunRequestInit
      return Response.json({ data: { __typename: "Query" } })
    }) as typeof globalThis.fetch

    const client = createGQLClient(1234, "token")
    await client.request("query { __typename }")

    assert.equal(requestInit?.timeout, false)
  })
})
