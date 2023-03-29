import { connect } from "@dagger.io/dagger"

import { writeFile } from "fs/promises"

// initialize Dagger client
connect(async (client) => {

  const secretEnv = client.setSecret("my-secret-env", "secret value here")
  const secretFile = client.setSecret("my-secret-file", "secret file content here")

  // dump secrets to console
  const output = await client.
		container().
		from("alpine:3.17").
		withSecretVariable("MY_SECRET_VAR", secretEnv).
		withMountedSecret("/my_secret_file", secretFile).
		withExec(["sh", "-c", `echo -e "secret env data: $MY_SECRET_VAR || secret file data: "; cat /my_secret_file`]).
		stdout()

  console.log(output)
}, {LogOutput: process.stderr})
