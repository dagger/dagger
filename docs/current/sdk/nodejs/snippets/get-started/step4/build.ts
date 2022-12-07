import Client, { connect } from "@dagger.io/dagger"

// initialize Dagger client
connect(async (client: Client) => {
  // highlight-start
  // get reference to the local project
  const source = client.host().directory(".", { exclude: ["node_modules/"] })

  // get Node image
  const node = client.container().from("node:16")

  // mount cloned repository into Node image
  const runner = client
    .container(node)
    .withMountedDirectory("/src", source)
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
