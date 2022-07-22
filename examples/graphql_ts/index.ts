import { client, DaggerServer, gql } from "@dagger.io/dagger";
import * as fs from "fs";

const resolvers = {
  Query: {
    echo: async (
      _: any,
      args: { in: string; fs: string },
    ) => {
      fs.readdirSync("/mnt/fs").forEach((file) => {
        console.log("look: ", file);
      });

      const query = gql`{
        alpine {
          build(pkgs:["jq"])
        }
      }`;

      const data = await client.request(query);

      return {
        fs: data.alpine.build,
        out: args.in,
      };
    },
  },
};

const server = new DaggerServer({
  typeDefs: gql(fs.readFileSync("/dagger.graphql", "utf8")),
  resolvers
})

server.run();
