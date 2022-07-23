import axios, { AxiosInstance } from "axios";
import { buildAxiosFetch } from "@lifeomic/axios-fetch";
import { GraphQLClient, gql } from "graphql-request";

import { Response } from "node-fetch";
// @ts-expect-error node-fetch doesn't exactly match the Response object, but close enough.
global.Response = Response;

export const client = new GraphQLClient("http://fake.invalid/graphql", {
  fetch: buildAxiosFetch(
    axios.create({
      socketPath: "/dagger.sock",
      timeout: 3600e3,
    })
  ),
});

export class Client {
  private client: AxiosInstance;

  constructor() {
    this.client = axios.create({
      socketPath: "/dagger.sock",
      timeout: 3600e3,
    });
  }

  public async do(payload: string): Promise<any> {
    const response = await this.client.post(
      `http://fake.invalid/graphql`,
      payload,
      { headers: { "Content-Type": "application/graphql" } }
    );
    return response;
  }
}
