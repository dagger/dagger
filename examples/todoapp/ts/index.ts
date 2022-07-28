import { client, DaggerServer, gql, Secret, FS } from "@dagger.io/dagger";
import { getSdk as netlifySdk } from "./gen/netlify/netlify.js";
import { getSdk as yarnSdk } from "./gen/yarn/yarn.js";
import { getSdk as todoappSdk } from "./gen/todoapp/todoapp.js";

import * as fs from "fs";

const netlify = netlifySdk(client);
const yarn = yarnSdk(client);
const self = todoappSdk(client);

const resolvers = {
  Query: {
    /*
     * Build the todoapp
     */
    build: async (parent: any, args: { src: FS }) => {
      return await yarn
        .Script({
          source: args.src,
          name: "build",
        })
        .then((res: any) => res.yarn.script);
    },

    /*
     * Test the todoapp
     */
    test: async (parent: any, args: { src: FS }) => {
      return await yarn
        .Script({
          source: args.src,
          name: "test",
        })
        .then((res: any) => res.yarn.script);
    },

    /*
     * Build and test the todoapp, if those pass then deploy it to Netlify
     */
    deploy: async (parent: any, args: { src: FS; token: Secret }) => {
      const built = await Promise.all([
        self.Build({ src: args.src }).then((res: any) => res.todoapp.build),
        self.Test({ src: args.src }).then((res: any) => res.todoapp.test),
      ]).then((results: any) => results[0]);

      return await netlify
        .Deploy({
          contents: built,
          subdir: "build",
          siteName: "test-cloak-netlify-deploy",
          token: args.token,
        })
        .then((res: any) => res.netlify.deploy);
    },
  },
};

const server = new DaggerServer({
  typeDefs: gql(fs.readFileSync("/schema.graphql", "utf8")),
  resolvers,
});

server.run();
