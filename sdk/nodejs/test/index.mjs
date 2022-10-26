import { gql, Engine } from "@dagger.io/dagger";

const engine = new Engine();

describe('NodeJS sdk', function () {
  it('Run a query to make sure it doesn\'t fail', function (done) {
    this.timeout(60000);
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
});
