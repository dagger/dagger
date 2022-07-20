import { DaggerServer, gql } from "./server";

const typeDefs = gql`
  scalar FS

  type Echo {
    fs: FS!
  }

  type GraphQLTS {
    echo(in: String!): Echo!
  }

  type Query {
    graphql_ts: GraphQLTS!
  }
`;

const resolvers = {
  Query: {
    graphql_ts: () => ({
      echo: (_: any, args: { in: string }) => {
        return {
          fs: "eyJQQiI6bnVsbCwiUXVlcnkiOiJ7XG4gIGFscGluZSB7XG4gICAgYnVpbGQocGtnczogW1wiY3VybFwiXSlcbiAgfVxufSIsIlZhcnMiOnt9fQ==",
        };
      },
    }),
  },
};

const server = new DaggerServer({ typeDefs, resolvers });

server.run();
