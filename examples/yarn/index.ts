import { client, DaggerServer, gql } from "@dagger.io/dagger";

const resolvers = {
  Yarn: {
    // TODO: support actual types for the new scalars like DirectoryID
    script: async (args: { source: string; runArgs: Array<string> }) => {
      const cacheId = await client
        .request(
          gql`
            {
              cacheVolume(key: "yarn-cache") {
                id
              }
            }
          `
        )
        .then((result: any) => result.cacheVolume.id);

      // NOTE: running install and then run is a great example of how explicit dependencies are no longer an issue
      const cmd = ["yarn", "run", ...args.runArgs];
      const yarnRun = await client
        .request(
          gql`
            query YarnRun(
              $source: DirectoryID!
              $cacheId: CacheID!
              $cmd: [String!]
            ) {
              alpine {
                build(pkgs: ["yarn", "git", "openssh-client"]) {
                  withMountedDirectory(path: "/src", source: $source) {
                    withMountedCache(path: "/cache", cache: $cacheId) {
                      withWorkdir(path: "/src") {
                        withEnvVariable(
                          name: "YARN_CACHE_FOLDER"
                          value: "/cache"
                        ) {
                          exec(args: ["yarn", "install"]) {
                            exec(args: $cmd) {
                              directory(path: "/src") {
                                id
                              }
                            }
                          }
                        }
                      }
                    }
                  }
                }
              }
            }
          `,
          {
            source: args.source,
            cacheId,
            cmd,
          }
        )
        .then(
          (result: any) =>
            result.alpine.build.withMountedDirectory.withMountedCache
              .withWorkdir.withEnvVariable.exec.exec.directory
        );
      return yarnRun;
    },
  },
  Directory: {
    yarn: async (args: { runArgs: Array<string> }, parent: { id: string }) => {
      return resolvers.Yarn.script({
        source: parent.id,
        runArgs: args.runArgs,
      });
    },
  },
};

const server = new DaggerServer({
  resolvers,
});

server.run();
