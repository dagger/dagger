import { connect, Client } from "@dagger.io/dagger"
import * as fs from "fs"

const files = ["foo.txt", "bar.txt", "baz.rar"]
let count = 1

for (const file of files) {
  fs.writeFileSync(file, count.toString())
  count = count + 1
}

connect(
  async (client: Client) => {
    const entries = await client
      .host()
      .directory(".", { include: ["*.rar"] })
      .entries()
    console.log(entries)
  },
  { LogOutput: process.stderr },
)
