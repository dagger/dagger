import { DaggerServer } from "./server";

const resolvers = {
  Query: {
    echo: async (
      parent: any,
      args: { in: string },
      context: any,
      info: any
    ) => {
      await context.dagger.do(`mutation{
        import(ref:"alpine") {
          name
        }
      }`);

      const input = `{
        alpine {
          build(pkgs:["curl"])
        }
      }`;

      const output = await context.dagger.do(input);
      return {
        fs: output.data.data.alpine.build,
      };
    },
  },
};

const server = new DaggerServer({ resolvers });

server.run();
