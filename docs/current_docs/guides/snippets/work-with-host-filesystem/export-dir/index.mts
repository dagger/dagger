import { connect, Client } from "@dagger.io/dagger"
import * as fs from "fs"
import * as os from "os"
import * as path from "path"

const hostdir = os.tmpdir()

connect(
  async (client: Client) => {
    await client
      .container()
      .from("alpine:latest")
      .withWorkdir("/tmp")
      .withExec(["wget", "https://dagger.io"])
      .directory(".")
      .export(hostdir)

    const contents = fs.readFileSync(path.join(hostdir, "index.html"))

    console.log(contents.toString())
  },
  { LogOutput: process.stderr },
)
