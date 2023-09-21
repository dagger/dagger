import { connect } from "@dagger.io/dagger"

connect(
  async (client) => {
    // use a node:18 container
    // mount the source code directory on the host
    // at /src in the container
    // mount the cache volume to persist dependencies
    const source = client
      .container()
      .from("node:18")
      .withDirectory("/src", client.host().directory("."))
      .withMountedCache("/src/node_modules", client.cacheVolume("node"))

    // set the working directory in the container
    // install application dependencies
    const runner = await source
      .withWorkdir("/src")
      .withExec(["npm", "install"])
      .sync()

    console.log(await runner.id())
  },
  { LogOutput: process.stderr }
)
