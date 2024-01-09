import { connect, Client } from "@dagger.io/dagger"

connect(
  async (client: Client) => {
    // get build context directory
    const contextDir = client.host().directory("/projects/myapp")

    // get Dockerfile in different filesystem location
    const dockerfilePath = "/data/myapp/custom.Dockerfile"
    const dockerfile = client.host().file(dockerfilePath)

    // add Dockerfile to build context directory
    const workspace = contextDir.withFile("custom.Dockerfile", dockerfile)

    // build using Dockerfile
    // publish the resulting container to a registry
    const imageRef = await client
      .container()
      .build(workspace, { dockerfile: "custom.Dockerfile" })
      .publish("ttl.sh/hello-dagger-" + Math.floor(Math.random() * 10000000))
    console.log(`Published image to: ${imageRef}`)
  },
  { LogOutput: process.stderr }
)
