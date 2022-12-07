import { connect } from "@dagger.io/dagger"

// initialize Dagger client
connect(async (client) => {
  // highlight-start
  // Set Node versions against which to test and build
  const nodeVersions = ["12", "14", "16"]
  // highlight-end

  // get reference to the local project
  const source = client.host().directory(".", { exclude: ["node_modules/"] })

  // highlight-start
  // for each Node version
  for (const nodeVersion of nodeVersions) {
    // get Node image
    const node = client.container().from(`node:${nodeVersion}`)
    // highlight-end

    // mount cloned repository into Node image
    const runner = client
      .container(node)
      .withMountedDirectory("/src", source)
      .withWorkdir("/src")
      .withExec(["npm", "install"])

    // run tests
    await runner.withExec(["npm", "test", "--", "--watchAll=false"]).exitCode()

    // highlight-start
    // build application using specified Node version
    // write the build output to the host
    await runner
      .withExec(["npm", "run", "build"])
      .directory("build/")
      .export(`./build-node-${nodeVersion}`)
  }
  // highlight-end
})
