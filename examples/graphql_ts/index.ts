import { DaggerServer } from "./server";

const resolvers = {
  Query: {
    echo: async (
      parent: any,
      args: { in: string },
      context: any,
      info: any
    ) => {
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
