import { Client, Container } from "@dagger.io/dagger"

// get base image
function base(client: Client): Container {
  return client.container().from("alpine:latest")
}

// run command in base image
export async function version(client: Client): Promise<string> {
  return base(client).withExec(["cat", "/etc/alpine-release"]).stdout()
}
