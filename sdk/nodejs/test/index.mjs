import { gql, Engine } from "@dagger.io/dagger";

const engine = new Engine();

engine.run(async (client) => {
  // Just run a query to make sure it doesn't fail
  await client
    .request(
      gql`
        {
          host {
            workdir {
              read {
                id
              }
            }
          }
        }
      `
    )
    .then((result) => result.host.workdir.read.id);
  console.log("Success!");
});

