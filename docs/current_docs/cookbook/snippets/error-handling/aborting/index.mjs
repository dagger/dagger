import { connect, GraphQLRequestError } from "@dagger.io/dagger"

connect(
  async (client) => {
    try {
      await test(client)
    } catch (e) {
      if (e instanceof GraphQLRequestError) {
        // If it's an API error, just show the error message.
        console.error(e.toString())
      } else {
        // Otherwise, show the full stack trace for debugging.
        console.error(e)
      }
      // Abort script with non-zero exit code.
      process.exit(1)
    }

    console.log("Test passed!")
  },
  { LogOutput: process.stderr },
)

async function test(client) {
  await client
    .container()
    .from("alpines")
    // ERROR: cat: read error: Is a directory
    .withExec(["cat", "/"])
    .sync()
}
