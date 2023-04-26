import { connect } from "@dagger.io/dagger"

// create Dagger client
connect(async (client) => {

  // create and publish image with annotations
	const container = client.container().
    from("alpine").
    withLabel("org.opencontainers.image.title", "my-alpine").
    withLabel("org.opencontainers.image.version", "1.0").
    withLabel("org.opencontainers.image.created", new Date())

  const addr = await container.publish("localhost:5000/my-alpine")

  console.log(addr)

}, {LogOutput: process.stderr})
