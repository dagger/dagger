import { DaggerServer, gql } from './server'

const typeDefs = gql`
  type Echo {
    message: String
  }

  type Query {
    echo(in: String!): Echo
  }
`;

const resolvers = {
  Query: {
    echo: (_: any, args: { in: string }) => ({ message: args.in }),
  },
};

const server = new DaggerServer({ typeDefs, resolvers });

server.run()
