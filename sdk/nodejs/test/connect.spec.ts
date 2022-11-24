import { connect } from "../connect.js"
import assert from "assert"

describe("NodeJS sdk", function () {
  it("Connect to local engine and execute a simple query to make sure it does not fail", async function () {
    this.timeout(60000)

    await connect(async (client) => {
      const result = await client
        .container()
        .from("alpine")
        .withExec(["apk", "add", "curl"])
        .withExec(["curl", "https://dagger.io/"])
        .stdout()

      assert.ok(result.length > 10000)
    })
  })
})
