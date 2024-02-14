import { connect } from "@dagger.io/dagger"

// initialize Dagger client
connect(
  async (client) => {
    // set secret as string value
    const secret = client.setSecret("password", "DOCKER-HUB-PASSWORD")

    // create container
    const c = client
      .container()
      .from("nginx:1.23-alpine")
      .withNewFile("/usr/share/nginx/html/index.html", {
        contents: "Hello from Dagger!",
        permissions: 0o400,
      })

    // use secret for registry authentication
    const addr = await c
      .withRegistryAuth("docker.io", "DOCKER-HUB-USERNAME", secret)
      .publish("DOCKER-HUB-USERNAME/my-nginx")

    // print result
    console.log(`Published at: ${addr}`)
  },
  { LogOutput: process.stderr },
)
