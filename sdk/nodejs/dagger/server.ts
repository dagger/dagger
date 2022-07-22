import { gql } from "apollo-server";
import {
  GraphQLOptions,
  ApolloServerBase,
  runHttpQuery,
  Config,
} from "apollo-server-core";
import { Request, Headers } from "apollo-server-env";
import { Client } from "./client";

import * as fs from "fs";

export interface DaggerContext {
  dagger: Client
}

export class DaggerServer<
  ContextFunctionParams = DaggerContext,
  > extends ApolloServerBase<ContextFunctionParams> {
  constructor(config: Config) {
    config.context = () => ({
      dagger: new Client(),
    });
    super(config);
  }

  async createGraphQLServerOptions(): Promise<GraphQLOptions> {
    const contextParams: DaggerContext = { dagger: new Client() };
    return super.graphQLServerOptions(contextParams);
  }

  private async query(input: Record<string, any>): Promise<string> {
    const { graphqlResponse } = await runHttpQuery(
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
