import { client, DaggerServer, SecretID, FSID } from "@dagger.io/dagger";
import { getSdk as netlifySdk } from "./gen/netlify/netlify.js";
import { getSdk as yarnSdk } from "./gen/yarn/yarn.js";
import { getSdk as todoappSdk } from "./gen/todoapp/todoapp.js";

const netlify = netlifySdk(client);
const yarn = yarnSdk(client);
const self = todoappSdk(client);

const resolvers = {
  Todoapp: {
    /*
     * Build the todoapp
     */
    build: async (args: { src: FSID }) => {
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
    test: async (args: { src: FSID }) => {
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
    deploy: async (args: { src: FSID; token: SecretID }) => {
      const built = await Promise.all([
        self.Build({ src: args.src }).then((res: any) => res.todoapp.build),
        self.Test({ src: args.src }).then((res: any) => res.todoapp.test),
      ]).then((results: any) => results[0]);

      return await netlify
        .Deploy({
          contents: built.id,
          subdir: "build",
          siteName: "test-cloak-netlify-deploy",
          token: args.token,
        })
        .then((res: any) => res.netlify.deploy);
    },
  },
  Query: {
    todoapp: async () => {
      return {};
    },
  },
  DeployURLs: {
    url: async (args: any, parent: any) => {
      return parent.url;
    },
    deployURL: async (args: any, parent: any) => {
      return parent.deployURL;
    },
    logsURL: async (args: any, parent: any) => {
      return parent.logsURL;
    },
  },
};

const server = new DaggerServer({ resolvers });

server.run();
