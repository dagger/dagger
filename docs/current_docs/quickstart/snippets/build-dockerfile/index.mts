import { connect, Client } from "@dagger.io/dagger"

connect(
  async (client: Client) => {
    // set build context
    const contextDir = client.host().directory(".")

    // build using Dockerfile
    // publish the resulting container to a registry
    const imageRef = await contextDir
      .dockerBuild()
      .publish("ttl.sh/hello-dagger-" + Math.floor(Math.random() * 10000000))
    console.log(`Published image to: ${imageRef}`)
  },
  { LogOutput: process.stderr }
)
