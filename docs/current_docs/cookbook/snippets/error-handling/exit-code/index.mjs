import { connect, GraphQLRequestError, ExecError } from "@dagger.io/dagger"

// Exit code for warnings.
const WARNING_EXIT = 5

const REPORT_CMD = `
echo "QA Checks"
echo "========="
echo "Check 1: PASS"
echo "Check 2: FAIL"
echo "Check 3: PASS"
exit 1
`

connect(
  async (client) => {
    try {
      // Will only abort if there's an unexpected error,
      // in which case the next pipeline won't execute.
      await test(client)

      console.log(await report(client))
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
  { LogOutput: process.stderr },
)

async function test(client) {
  try {
    await client
      .container()
      .from("alpine")
      // ERROR: cat: read error: Is a directory
      .withExec(["sh", "-c", "echo Skipped! >&2; exit 5"])
      .sync()
  } catch (e) {
    // Handle error from withExec here, but let other errors bubble up.
    if (e instanceof ExecError) {
      // Don't do anything when skipped.
      // Print message to stderr otherwise.
      if (e.exitCode !== WARNING_EXIT) {
        console.error("Test failed: %s", e.stderr)
      }
      return
    }
    // Rethrow other errors.
    throw e
  }
}

async function report(client) {
  // Get stdout even on non-zero exit code.
  try {
    return await client
      .container()
      .from("alpines") // ⚠️ typo! non-exec failure
      .withExec(["sh", "-c", REPORT_CMD])
      .stdout()
  } catch (e) {
    if (e instanceof ExecError) {
      // Not necessary to check for `e.exitCode != 0`.
      return e.stdout
    }
    // Rethrow other errors.
    throw e
  }
}
