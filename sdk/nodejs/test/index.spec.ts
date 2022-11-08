import { connect, Client } from "../index.js";
import assert from "assert";

describe('NodeJS sdk', function () {
  it('Run a query to make sure it doesn\'t fail', async function () {
    await connect(async (client: Client) => {
        // Just run a query to make sure it doesn't fail
        const res = await client.host().workdir().id()

        assert.ok(typeof res.id === "string")
      });
  });

  it('Connect to engine and execute a simple query to make sure it does not fail', async function () {
    // Use a different port to avoid collision with other tests.
    await connect(async (client: Client) => {
      const res = await client
        .container()
        .from({address: 'alpine'})
        .exec({args: ["apk", "add", "curl"]})
        .exec({args: ["curl", "https://dagger.io/"]})
        .stdout()
        .size()

      assert.ok(res.size > 10000)
    }, {Port: 8082});
  })
});
