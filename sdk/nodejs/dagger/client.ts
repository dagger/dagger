import axios, { AxiosInstance } from "axios";

export class Client {
  private client: AxiosInstance;

  constructor() {
    this.client = axios.create({
      socketPath: "/dagger.sock",
      timeout: 15e3,
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
