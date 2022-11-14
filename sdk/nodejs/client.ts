import axios from "axios";
import { buildAxiosFetch } from "@lifeomic/axios-fetch";
import { GraphQLClient } from "graphql-request";

import { Response } from "node-fetch";

// @ts-expect-error node-fetch doesn't exactly match the Response object, but close enough.
global.Response = Response;

// TODO(Tomchv): useless so we can remove it later.
export const client = new GraphQLClient("http://fake.invalid/query", {
  fetch: buildAxiosFetch(
    axios.create({
      socketPath: "/dagger.sock",
      timeout: 3600e3,
    })
  ),
});

export class Client {
  private client: GraphQLClient;

  /**
   * creates a new Dagger Typescript SDK GraphQL client.
   */
  constructor(client: GraphQLClient) {
    this.client = client;
  }

  /**
   * do takes a GraphQL query payload as parameter and send it
   * to Cloak server to execute every operation's in it.
   */
  public async do(payload: string): Promise<any> {
    return await this.client.request(payload);
  }
}

export class FSID {
  serial: string;

  constructor(serial: string) {
    this.serial = serial;
  }

  toString(): string {
    return this.serial;
  }

  toJSON(): string {
    return this.serial;
  }
}

export class SecretID {
  serial: string;

  constructor(serial: string) {
    this.serial = serial;
  }

  toString(): string {
    return this.serial;
  }

  toJSON(): string {
    return this.serial;
  }
}
