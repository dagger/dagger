import { connect } from "@dagger.io/dagger"

connect(async (client) => {

  // use a node:16-slim container
  // mount the source code directory on the host
  // at /src in the container
  const source = client.container()
    .from("node:16-slim")
    .withMountedDirectory('/src', client.host().directory('.', { exclude: ["node_modules/", "ci/"] }))

  // set the working directory in the container
  // install application dependencies
  const runner = source
    .withWorkdir("/src")
    .withExec(["npm", "install"])

  // run application tests
  const test = runner
    .withExec(["npm", "test", "--", "--watchAll=false"])

  // highlight-start
  // build application
  // write the build output to the host
  const buildDir = test
    .withExec(["npm", "run", "build"])
    .directory("./build")

  await buildDir.export("./build")
  // highlight-end

  const e = await buildDir.entries()

  console.log("build dir contents:\n", e)

}, { LogOutput: process.stdout })
