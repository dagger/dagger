import { DaggerServer } from "./server";
import * as fs from "fs";

const resolvers = {
  Query: {
    echo: async (
      parent: any,
      args: { in: string; fs: string },
      context: any,
      info: any
    ) => {
      fs.readdirSync("/mnt/fs").forEach((file) => {
        console.log("look: ", file);
      });

      const input = `{
        alpine {
          build(pkgs:["jq"])
        }
      }`;

      const output = await context.dagger.do(input);
      return {
        fs: output.data.data.alpine.build,
        out: args.in,
      };
    },
  },
};

const server = new DaggerServer({ resolvers });

server.run();
