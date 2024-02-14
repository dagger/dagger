import { connect } from "@dagger.io/dagger"

// create Dagger client
connect(
  async (client) => {
    // invalidate cache to force execution
    // of second withExec() operation
    const output = await client
      .pipeline("test")
      .container()
      .from("alpine")
      .withExec(["apk", "add", "curl"])
      .withEnvVariable("CACHEBUSTER", Date.now().toString())
      .withExec(["apk", "add", "zip"])
      .stdout()

    console.log(output)
  },
  { LogOutput: process.stderr },
)
