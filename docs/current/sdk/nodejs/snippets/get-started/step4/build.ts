import Client, { connect } from "@dagger.io/dagger"

 // initialize Dagger client
connect(async (client: Client) => {

  // Set Node versions to test
  const nodeVersions = ["12", "14", "16"]

  // get reference to the local project
  const source = await client.host().workdir().id();

  for(const nodeVersion of nodeVersions) {

    // get Node image
    const node = await client
      .container()
      .from({ address: `node:${nodeVersion}` })
      .id()

    // mount cloned repository into node image
    const runTest = client
      .container({ id: node.id })
      .withMountedDirectory({ path: "/src", source: source.id })
      .withWorkdir({ path: "/src" })

    // Run test for earch node version
    await runTest
      .exec({ args: ["npm", "test", "--", "--watchAll=false"] })
      .exitCode()

    // Run build for each node version
    // and write the contents of the directory on the host
    await client
      .container({ id: node.id })
      .withMountedDirectory({ path: "/src", source: source.id })
      .withWorkdir({ path: "/src" })
      .exec({ args: ["npm", "run", "build"] })
      .directory({path: "build/"})
      .export({path: `./build-node-${nodeVersion}`})
  }
});
