import Client, { connect } from "@dagger.io/dagger"
import * as fs from "fs"
import * as os from "os"
import * as path from "path"

const dir = os.tmpdir()
const files = ["foo.txt", "bar.txt", "baz.rar"]
let count = 1

for (const file of files) {
  fs.writeFileSync(path.join(dir, file), count.toString())
  count = count + 1
}

connect(
  async (client: Client) => {
    const entries = await client
      .host()
      .directory(".", { exclude: ["*.txt"] })
      .entries()
    console.log(entries)
  },
  { LogOutput: process.stderr, Workdir: dir }
)
