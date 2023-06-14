import Client, { serveCommands } from "@dagger.io/dagger"

/**
 * Test doc
 * @param name test
 * @param age test
 * @returns test
 */
function foo(client: Client, name: string, age: string): string {
  return name + age
}

/**
 * Test Bar function with multiples params and different primitive types
 * @param toto Test toto param
 * @param bool Test bool param
 * @returns Concatenation of parameters
 */
async function bar(client: Client): Promise<string> {
  const result = await client
    .container()
    .from("alpine")
    .withExec(["echo", "hello world"])
    .stdout()

  console.log("result:", result)
  return result
}

serveCommands(foo, bar)
