import { dag } from '@dagger.io/dagger'
import * as dagger from '@dagger.io/dagger'

// initialize Dagger client
await dagger.connection(async () => {
    // set Node versions against which to test and build
    const nodeVersions = ["16", "18", "20"]

    // get reference to the local project
    const source = dag.host().directory(".", { exclude: ["node_modules/"] })

    // for each Node version
    for (const nodeVersion of nodeVersions) {
      // get Node image
      const node = dag.container().from(`node:${nodeVersion}`)

      // mount cloned repository into Node image
      const runner = node
        .withDirectory("/src", source)
        .withWorkdir("/src")
        .withExec(["npm", "install"])

      // run tests
      await runner.withExec(["npm", "test", "--", "--watchAll=false"]).sync()

      // build application using specified Node version
      // write the build output to the host
      await runner
        .withExec(["npm", "run", "build"])
        .directory("build/")
        .export(`./build-node-${nodeVersion}`)
    }
  },
  { LogOutput: process.stderr }
)
