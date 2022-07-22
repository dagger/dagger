import { client, DaggerServer, gql } from "@dagger.io/dagger";
import * as fs from "fs";
import { getSdk } from "./gen/alpine/alpine";

const resolvers = {
  Query: {
    echo: async (
      _: any,
      args: { in: string; fs: string },
    ) => {
      // By hand
      const query = gql`{
        alpine {
          build(pkgs:["jq"])
        }
      }`;
      const data = await client.request(query);
      console.log("alpine build", data.alpine.build);


      // With codegen
      const alpine = getSdk(client);
      const image = await alpine.Build({ pkgs: ["jq", "curl"] })

      return {
        fs: image.alpine.build,
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
