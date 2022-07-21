import Dagger from "dagger";

import { gql } from "apollo-server";
import {
  GraphQLOptions,
  ApolloServerBase,
  runHttpQuery,
  Config,
} from "apollo-server-core";
import { Request, Headers } from "apollo-server-env";

import * as fs from "fs";

export class DaggerServer extends ApolloServerBase {
  constructor(config: Config) {
    config.typeDefs = gql(fs.readFileSync("/dagger.graphql", "utf8"));
    config.context = () => ({
      dagger: new Dagger(),
    });
    super(config);
  }

  async createGraphQLServerOptions(): Promise<GraphQLOptions> {
    return super.graphQLServerOptions();
  }

  private async query(input: Record<string, any>): Promise<string> {
    const { graphqlResponse, responseInit } = await runHttpQuery(
      [],
      {
        method: "POST",
        options: () => this.createGraphQLServerOptions(),
        query: input,
        request: new Request("/graphql", {
          headers: new Headers(),
          method: "POST",
        }),
      },
      null
    );
    return graphqlResponse;
  }

  public run() {
    this.start();

    const inputs = JSON.parse(fs.readFileSync("/inputs/dagger.json", "utf8"));
    this.query(inputs).then((resp) =>
      fs.writeFileSync("/outputs/dagger.json", resp)
    );
  }
}
