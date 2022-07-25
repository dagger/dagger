import { client, DaggerServer, gql } from "@dagger.io/dagger";

import * as fs from "fs";

const resolvers = {
  Query: {
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
  typeDefs: gql(fs.readFileSync("/dagger.graphql", "utf8")),
  resolvers,
});

server.run();
