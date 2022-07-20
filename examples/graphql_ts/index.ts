import { DaggerServer, gql } from "./server";

const resolvers = {
  Query: {
    echo: (_: any, args: { in: string }) => {
      return {
        fs: "eyJQQiI6bnVsbCwiUXVlcnkiOiJ7XG4gIGFscGluZSB7XG4gICAgYnVpbGQocGtnczogW1wiY3VybFwiXSlcbiAgfVxufSIsIlZhcnMiOnt9fQ==",
      };
    },
  },
};

const server = new DaggerServer({ resolvers });

server.run();
