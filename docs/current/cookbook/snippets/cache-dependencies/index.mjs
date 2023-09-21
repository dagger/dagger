import { connect } from "@dagger.io/dagger"

connect(
  async (client) => {
    // create a cache volume
    const cache = client.cacheVolume("node")

    // use a node:18 container
    // mount the source code directory on the host
    // at /src in the container
    // mount the cache volume to persist dependencies
    const source = client
      .container()
      .from("node:18")
      .withDirectory("/src", client.host().directory("."))
      .withMountedCache("/src/node_modules", cache)

    // set the working directory in the container
    // install application dependencies
    const runner = await source.withWorkdir("/src").withExec(["npm", "install"]).sync()

    console.log(await runner.id())
  },
  { LogOutput: process.stderr }
)
