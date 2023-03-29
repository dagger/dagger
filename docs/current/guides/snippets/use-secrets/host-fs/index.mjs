import { connect } from "@dagger.io/dagger"

import { readFile } from "fs/promises"

// initialize Dagger client
connect(async (client) => {
  // read file
  const file = await readFile("/home/USER/.config/gh/hosts.yml")

  // set secret to file contents
  const secret = client.setSecret("ghConfig", file.toString())

  // mount secret as file in container
  const out = await client.
    container().
    from("alpine:3.17").
    withExec(["apk", "add", "github-cli"]).
    withMountedSecret("/root/.config/gh/hosts.yml", secret).
    withWorkdir("/root").
    withExec(["gh", "auth", "status"]).
    stdout()

  // print result
  console.log(out)
}, {LogOutput: process.stderr})
