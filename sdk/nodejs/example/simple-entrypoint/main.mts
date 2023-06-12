import Client, { serveCommands } from "@dagger.io/dagger"

/**
 * Test doc
 * @param client Client
 * @param name test
 * @param age test
 * @returns test
 */
function foo(client: Client, name: string, age: string): string {
  return name + age
}

serveCommands(foo)
