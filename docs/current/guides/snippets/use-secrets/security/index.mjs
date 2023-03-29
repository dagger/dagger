import { connect } from "@dagger.io/dagger"

import { writeFile } from "fs/promises"

// initialize Dagger client
connect(async (client) => {

  // set a test host environment variable
  process.env["MY_SECRET_VAR"] = "secret value here"

  // set a test host file
  await writeFile("my_secret_file", "secret file content here")

	// load secrets
	const secretEnv = client.host().envVariable("MY_SECRET_VAR").secret()
	const secretFile = client.host().directory(".").file("my_secret_file").secret()

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
