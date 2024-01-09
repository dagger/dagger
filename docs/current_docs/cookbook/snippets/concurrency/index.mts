import { Client, connect } from "@dagger.io/dagger"
import { randomInt } from "crypto"

async function longTimeTask(c: Client): Promise<void> {
  await c
    .container()
    .from("alpine")
    .withExec(["sleep", randomInt(0, 10).toString()])
    .withExec(["echo", "task done"])
    .sync()
}

connect(
  async (client: Client) => {
    await Promise.all([
      longTimeTask(client),
      longTimeTask(client),
      longTimeTask(client),
    ])
  },
  { LogOutput: process.stderr }
)
