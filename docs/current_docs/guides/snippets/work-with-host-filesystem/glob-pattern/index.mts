import { connect, Client } from "@dagger.io/dagger"
import * as fs from "fs"
import * as path from "path"

const workdir = process.cwd()
let folder = workdir + path.sep

for (const subdir of ["foo", "bar", "baz"]) {
  folder = path.join(folder, subdir)
  fs.mkdirSync(folder)

  for (const file of [".txt", ".out", ".rar"]) {
    fs.writeFileSync(path.join(folder, subdir + file), subdir)
  }
}

connect(
  async (client: Client) => {
    const daggerdir = await client.host().directory(workdir, {
      include: ["**/*.rar", "**/*.txt"],
      exclude: ["**.out"],
    })

    folder = "." + path.sep
    for (const dir of ["foo", "bar", "baz"]) {
      folder = path.join(folder, dir)
      const entries = await daggerdir.entries({ path: folder })
      console.log(entries)
    }
  },
  { LogOutput: process.stderr },
)
