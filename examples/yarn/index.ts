import { client, DaggerServer, gql, FSID } from "@dagger.io/dagger";

const resolvers = {
  Yarn: {
    script: async (args: { source: FSID; name: string }) => {
      const base = await client
        .request(
          gql`
            {
              alpine {
                build(pkgs: ["yarn", "git"]) {
                  id
                }
              }
            }
          `
        )
        .then((result: any) => result.alpine.build);
      // console.log("base: ", base);

      // NOTE: running install and then run is a great example of how explicit dependencies are no longer an issue
      const yarnInstall = await client
        .request(
          gql`
            {
              core {
                filesystem(id: "${base.id}") {
                  exec(input: {
                    args: ["yarn", "install"], 
                    mounts: [{path: "/src", fs: "${args.source}"}],
                    workdir: "/src",
                  }) {
                    mount(path: "/src") {
                      id
                    }
                  }
                }
              }
            }
          `
        )
        .then((result: any) => result.core.filesystem.exec.mount);
      // console.log("yarnInstall: ", yarnInstall);

      const yarnRun = await client
        .request(
          gql`
            {
              core {
                filesystem(id: "${base.id}") {
                  exec(input: {
                    args: ["yarn", "run", "${args.name}"],
                    mounts: [{path: "/src", fs: "${yarnInstall.id}"}],
                    workdir: "/src",
                  }) {
                    mount(path: "/src") {
                      id
                    }
                  }
                }
              }
            }
          `
        )
        .then((result: any) => result.core.filesystem.exec.mount);
      // console.log("yarnInstall: ", yarnInstall);

      return yarnRun;
    },
  },
};

const server = new DaggerServer({
  resolvers,
});

server.run();
