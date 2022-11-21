import { connect } from "@dagger.io/dagger"

// initialize Dagger client
connect(async (client) => {
  // highlight-start
  // get reference to the local project
  const source = await client.host().directory(".", ["node_modules/"]).id()

  // get Node image
  const node = await client.container().from("node:16").id()

  // mount cloned repository into Node image
  const runner = client
    .container(node.id)
    .withMountedDirectory("/src", source.id)
    .withWorkdir("/src")
    .withExec(["npm", "install"])

  // run tests
  await runner.withExec(["npm", "test", "--", "--watchAll=false"]).exitCode()

  // build application
  // write the build output to the host
  await runner
    .withExec(["npm", "run", "build"])
    .directory("build/")
    .export("./build")
  // highlight-end
})
