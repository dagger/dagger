import { connect } from "@dagger.io/dagger"

// create Dagger client
connect(
  async (client) => {
    // define tags
    const tags = ["latest", "1.0-alpine", "1.0", "1.0.0"]

    if (!process.env.DOCKERHUB_USERNAME) {
      console.log("DOCKERHUB_USERNAME environment variable must be set")
      process.exit()
    }
    if (!process.env.DOCKERHUB_PASSWORD) {
      console.log("DOCKERHUB_PASSWORD environment variable must be set")
      process.exit()
    }
    const username = process.env.DOCKERHUB_USERNAME
    const password = process.env.DOCKERHUB_PASSWORD

    // set secret as string value
    const secret = client.setSecret("password", password)

    // create and publish image with multiple tags
    const container = client.container().from("alpine")

    for (var tag in tags) {
      let addr = await container
        .withRegistryAuth("docker.io", username, secret)
        .publish(`${username}/my-alpine:${tags[tag]}`)
      console.log(`Published at: ${addr}`)
    }
  },
  { LogOutput: process.stderr },
)
