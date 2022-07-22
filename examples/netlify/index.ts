import { DaggerServer } from "@dagger.io/dagger";
import * as fs from "fs";

const resolvers = {
  Query: {
    deploy: async (
      context: any,
      args: { contents: string; site: string; token: string }
    ) => {
      fs.readdirSync("/mnt/contents").forEach((file) => {
        console.log("look: ", file);
      });

      return {
        url: "url",
        deployUrl: "deployUrl",
        logsUrl: "logsUrl",
      };
    },
  },
};

const server = new DaggerServer({ resolvers });

server.run();
