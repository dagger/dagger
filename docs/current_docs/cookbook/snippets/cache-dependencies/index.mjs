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
      .withWorkdir("/src")
      .withMountedCache(
        "/src/node_modules",
        client.cacheVolume("node-18-myapp-myenv"),
      )
      .withMountedCache("/root/.npm", client.cacheVolume("node-18"))

    // set the working directory in the container
    // install application dependencies
    await source.withExec(["npm", "install"]).sync()
  },
  { LogOutput: process.stderr },
)
