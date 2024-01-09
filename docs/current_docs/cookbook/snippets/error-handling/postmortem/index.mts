import { connect, Client, GraphQLRequestError } from "@dagger.io/dagger"

const SCRIPT = `#!/bin/sh
echo "Test Suite"
echo "=========="
echo "Test 1: PASS" >> report.txt
echo "Test 2: FAIL" >> report.txt
echo "Test 3: PASS" >> report.txt
exit 1
`

connect(
  async (client: Client) => {
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
  },
  { LogOutput: process.stderr }
)

async function test(client) {
  //  If any one of these steps fails, it's an unexpected error so we don't
  //  need to handle anything here.

  // The result of `sync` is the container, which allows continued chaining.
  const ctr = await client
    .container()
    .from("alpine")
    // Add script with execution permission to simulate a testing tool.
    .withNewFile("run-tests", { contents: SCRIPT, permissions: 0o750 })
    // If the exit code isn't needed: "run-tests; true
    .withExec(["sh", "-c", "/run-tests; echo -n $? > /exit_code"])
    .sync()

  // Save report locally for inpspection.
  await ctr.file("report.txt").export("report.txt")

  // Use the saved exit code to determine if the tests passed.
  const exitCode = await ctr.file("exit_code").contents()

  if (exitCode !== "0") {
    console.error("Tests failed!")
  } else {
    console.log("Tests passed!")
  }
}
