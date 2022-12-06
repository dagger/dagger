import { connect } from "../connect.js"
import assert, { AssertionError } from "assert"
import { GraphQLRequestError } from "../common/errors/index.js"

describe("NodeJS sdk", function () {
  it("Connect to local engine and execute a simple query to make sure it does not fail", async function () {
    this.timeout(60000)

    await connect(async (client) => {
      const result = await client
        .container()
        .from("alpine")
        .withExec(["apk", "add", "curl"])
        .withExec(["curl", "https://dagger.io/"])
        .exitCode()

      assert.ok(result === 0)
    })
  })

  it("throws error", async function () {
    this.timeout(60000)

    try {
      await connect(async (client) => {
        await client.container().from("alpine").file("unknown_file").contents()

        assert.fail("Should throw error before reaching this")
      })
    } catch (e) {
      if (e instanceof AssertionError) {
        throw e
      }
      assert(e instanceof GraphQLRequestError)
    }
  })
})
