import Client, { serveCommands } from "@dagger.io/dagger"

serveCommands(hello, compute)

function hello(_: Client, name: string): string {
  return `Hello ${ name }`
}

async function compute(client: Client): Promise<string> {
  return client
    .container()
    .from("alpine")
    .withExec([ "echo", "hello world" ])
    .stdout()
}