import { connect, Client } from "@dagger.io/dagger"

connect(
  async (client: Client) => {
    const entries = await client.host().directory(".").entries()
    console.log(entries)
  },
  { LogOutput: process.stderr },
)
