import { client, DaggerServer, gql, FS } from "@dagger.io/dagger";
import * as fs from "fs";

const resolvers = {
  Query: {
    script: async (parent: any, args: { source: FS; name: string }) => {
      // TODO: update to use generated client instead of raw queries
      const base = await client
        .request(
          gql`
            {
              alpine {
                build(pkgs: ["yarn", "git"])
              }
            }
          `
        )
        .then((result: any) => result.alpine.build);
      // console.log("base: ", base);

      // TODO: get output of commands
      // NOTE: running install and then run is a great example of how explicit dependencies are no longer an issue
      const yarnInstall = await client
        .request(
          gql`
            {
              core {
                exec(input: {
                  args: ["yarn", "install"], 
                  mounts: [
                    {path: "/", fs: "${base}"},
                    {path: "/src", fs: "${args.source}"},
                  ],
                  workdir: "/src",
                }) { getMount(path: "/src") }
              }
            }
          `
        )
        .then((result: any) => result.core.exec.getMount);
      // console.log("yarnInstall: ", yarnInstall);

      const yarnRun = await client
        .request(
          gql`
            {
              core {
                exec(input: {
                  args: ["yarn", "run", "${args.name}"],
                  mounts: [
                    {path: "/", fs: "${base}"},
                    {path: "/src", fs: "${yarnInstall}"},
                  ],
                  workdir: "/src",
                }) { getMount(path: "/src") }
              }
            }
          `
        )
        .then((result: any) => result.core.exec.getMount);
      // console.log("yarnRun: ", yarnRun);

      return yarnRun;
    },
  },
};

const server = new DaggerServer({
  typeDefs: gql(fs.readFileSync("/schema.graphql", "utf8")),
  resolvers,
});

server.run();
