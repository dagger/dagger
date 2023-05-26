import { connect } from "@dagger.io/dagger"
import { v4 as uuidv4 } from 'uuid';

// create Dagger client
connect(async (client) => {

  // invalidate cache to force execution
  // of second withExec() operation
  const output = await client.pipeline("test").
    container().
    from("alpine").
    withExec(["apk", "add", "curl"]).
    withEnvVariable("CACHEBUSTER", uuidv4()).
    withExec(["apk", "add", "zip"]).
    stdout()

  console.log(output)

}, {LogOutput: process.stderr})
