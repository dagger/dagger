import { connect } from "@dagger.io/dagger"

// create Dagger client
connect(async (client) => {
  // define tags
  const tags = ["latest", "1.0-alpine", "1.0", "1.0.0"]

  // set secret as string value
  const secret = client.setSecret("password", "DOCKER-HUB-PASSWORD")

  // create and publish image with multiple tags
	const container = client.container().
    from("alpine")

  for (var tag in tags) {
    let addr = await container.
      withRegistryAuth("docker.io", "DOCKER-HUB-USERNAME", secret).
      publish(`DOCKER-HUB-USERNAME/my-alpine:${tags[tag]}`)
    console.log(`Published at: ${addr}`)
  }

}, {LogOutput: process.stderr})
