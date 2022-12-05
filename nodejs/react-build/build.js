import { connect } from "@dagger.io/dagger"

// initialize Dagger client
connect(async (client) => {
  // Set Node versions against which to test and build
  const nodeVersions = ["12", "14", "16"]

  // get reference to the local project
  const source = await client.host().directory(".", ["node_modules/"]).id()

  // for each Node version
  for (const nodeVersion of nodeVersions) {
    // get Node image
    const node = client.container().from(`node:${nodeVersion}`)

    // mount cloned repository into Node image
    const runner = client
      .container(node)
      .withMountedDirectory("/src", source)
      .withWorkdir("/src")
      .withExec(["npm", "install"])

    // run tests
    await runner.withExec(["npm", "test", "--", "--watchAll=false"]).exitCode()

    // build application using specified Node version
    // write the build output to the host
    await runner
      .withExec(["npm", "run", "build"])
      .directory("build/")
      .export(`./build-node-${nodeVersion}`)
  }
}, {LogOutput: process.stdout})
