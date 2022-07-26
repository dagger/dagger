import { client, DaggerServer, gql } from "@dagger.io/dagger";

import * as fs from "fs";

const resolvers = {
  Query: {
    /*
     * Build the todoapp
     */
    build: async (parent: any, args: { src: string }) => {
      return await client
        .request(
          gql`
            {
              yarn {
                script(source: "${args.src}", name: "build")
              }
            }
          `
        )
        .then((result: any) => result.yarn.script);
    },

    /*
     * Test the todoapp
     */
    test: async (parent: any, args: { src: string }) => {
      return await client
        .request(
          gql`
            {
              yarn {
                script(source: "${args.src}", name: "test")
              }
            }
          `
        )
        .then((result: any) => result.yarn.script);
    },

    /*
     * Build and test the todoapp, if those pass then deploy it to Netlify
     */
    deploy: async (parent: any, args: { src: string; token: string }) => {
      const built = await Promise.all([
        client
          .request(
            gql`
            {
              todoapp {
                build(src: "${args.src}")
              }
            }
          `
          )
          .then((result: any) => result.todoapp.build),
        client
          .request(
            gql`
            {
              todoapp {
                test(src: "${args.src}")
              }
            }
          `
          )
          .then((result: any) => result.todoapp.test),
      ]).then((results: any) => results[0]);

      return await client
        .request(
          gql`
            {
              netlify {
                deploy(contents: "${built}", subdir: "build", siteName: "test-cloak-netlify-deploy", token: "${args.token}") {
                  url
                  deployUrl
                }
              }
            }
          `
        )
        .then((result: any) => result.netlify.deploy);
    },
  },
};

const server = new DaggerServer({
  typeDefs: gql(fs.readFileSync("/schema.graphql", "utf8")),
  resolvers,
});

server.run();
