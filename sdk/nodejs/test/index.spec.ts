import { gql, Engine, connect } from "../index.js";
import assert from "assert";

const engine = new Engine({});

describe('NodeJS sdk', function () {
  it('Run a query to make sure it doesn\'t fail', function (done) {
    //DEPRECATED
    engine.run(async (client) => {
        // Just run a query to make sure it doesn't fail
        await client
          .request(
            gql`
              {
                host {
                  workdir {
                    id
                  }
                }
              }
            `
          ).then(done());
      });
  });

  it('Connect to engine and execute a simple query to make sure it does not fail', async function () {

    // Use a different port to avoid collision with other tests.
    await connect(async (client) => {
      const res = await client
        .container()
        .from({address: 'alpine'})
        .exec({args: ["apk", "add", "curl"]})
        .exec({args: ["curl", "https://dagger.io/"]})
        .stdout()
        .size()

      assert.ok(res.size > 10000)
    });
  })
});
