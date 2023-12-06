import { Client, Container } from "@dagger.io/dagger"

export class Alpine {
  private client: Client

  // initialize pipeline class
  constructor(client: Client) {
    this.client = client
  }

  // get base image
  private base(): Container {
    return this.client.container().from("alpine:latest")
  }

  // run command in base image
  public async version(): Promise<string> {
    return this.base().withExec(["cat", "/etc/alpine-release"]).stdout()
  }
}
