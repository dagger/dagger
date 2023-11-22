import { connect } from "@dagger.io/dagger"

connect(
  async (client) => {
    // build container in one pipeline
    const ctr = await client
      .pipeline("Test")
      .container()
      .from("alpine")
      .withExec(["apk", "add", "curl"])
      .sync()

    // get container ID
    const cid = await ctr.id()

    // use container in another pipeline via its ID
    await client
      .container({ id: cid })
      .pipeline("Build")
      .withExec(["curl", "https://dagger.io"])
      .sync()
  },
  { LogOutput: process.stderr }
)
