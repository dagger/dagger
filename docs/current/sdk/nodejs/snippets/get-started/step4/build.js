import { connect } from "@dagger.io/dagger"

// initialize Dagger client
connect(async (client) => {
  // highlight-start
  // get reference to the local project
  const source = client.host().directory(".", { exclude: ["node_modules/"] })

  // get Node image
  const node = client.container().from("node:16")

  // mount cloned repository into Node image
  const runner = node
    .withMountedDirectory("/src", source)
    .withWorkdir("/src")
    .withExec({ args: ["npm", "install"] })

  // run tests
  await runner.withExec({ args: ["npm", "test", "--", "--watchAll=false"] })
    .exitCode()

  // build application
  // write the build output to the host
  await runner
    .withExec({ args: ["npm", "run", "build"] })
    .directory("build/")
    .export("./build")
  // highlight-end
})
