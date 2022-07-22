import { DaggerServer } from "@dagger.io/dagger";
import * as fs from "fs";

const resolvers = {
  Query: {
    script: async (context: any, args: { source: string; name: string }) => {
      fs.readdirSync("/mnt/source").forEach((file) => {
        console.log("look: ", file);
      });

      return args.source;
    },
  },
};

const server = new DaggerServer({ resolvers });

server.run();
