import { gql, Engine, connect, getProvisioner } from "@dagger.io/dagger";

import assert from "assert";

const engine = new Engine();

describe("NodeJS sdk", function () {
  it("Connect to engine and execute a simple query to make sure it does not fail", async function () {
    this.timeout(60000);

    const client = await connect(engine);
    await client.do(gql`
      {
        container {
          from(address: "alpine") {
            exec(args: ["apk", "add", "curl"]) {
              exec(args: ["curl", "https://dagger.io/"]) {
                stdout {
                  size
                }
              }
            }
          }
        }
      }
    `);
    assert.ok(res.container.from.exec.exec.stdout.size > 10000);
  });
});
