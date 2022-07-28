import { gql } from "apollo-server";
import {
  GraphQLOptions,
  ApolloServerBase,
  runHttpQuery,
  Config,
} from "apollo-server-core";
import { Request, Headers } from "apollo-server-env";
import { GraphQLScalarType } from "graphql";
import { Client, FS, Secret } from "./client";

import * as fs from "fs";

export interface DaggerContext {
  dagger: Client;
}

export const FSScalar = new GraphQLScalarType({
  name: "FS",
  description: "Filesystem",
  serialize(value: any): any {
    switch (typeof value) {
      case "string":
        return value;
      case "object":
        return value.serial;
      default:
        throw new Error(`Cannot serialize ${value}`);
    }
  },
  parseValue(value: any): any {
    return new FS(value);
  },
  parseLiteral(ast: any): any {
    return new FS(ast.value);
  },
});

export const SecretScalar = new GraphQLScalarType({
  name: "Secret",
  description: "Secret",
  serialize(value: any): any {
    switch (typeof value) {
      case "string":
        return value;
      case "object":
        return value.serial;
      default:
        throw new Error(`Cannot serialize ${value}`);
    }
  },
  parseValue(value: any): any {
    return new Secret(value);
  },
  parseLiteral(ast: any): any {
    return new Secret(ast.value);
  },
});

export class DaggerServer<
  ContextFunctionParams = DaggerContext
> extends ApolloServerBase<ContextFunctionParams> {
  constructor(config: Config) {
    config.resolvers = {
      FS: FSScalar,
      Secret: SecretScalar,
      ...config.resolvers,
    };
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
    this.query(inputs).then((resp) => {
      fs.writeFileSync("/outputs/dagger.json", resp);
      if (JSON.parse(resp).errors) {
        console.error(JSON.parse(resp).errors);
        process.exit(1);
      }
    });
  }
}
