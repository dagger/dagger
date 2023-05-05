import { connect } from "@dagger.io/dagger"

// create Dagger client
connect(async (client) => {

  // create and publish image with annotations
	const container = client.container().
    from("alpine").
    withLabel("org.opencontainers.image.title", "my-alpine").
    withLabel("org.opencontainers.image.version", "1.0").
    withLabel("org.opencontainers.image.created", new Date()).
		WithLabel("org.opencontainers.image.source", "https://github.com/alpinelinux/docker-alpine").
		WithLabel("org.opencontainers.image.licenses", "MIT")

  const addr = await container.publish("ttl.sh/my-alpine")

  console.log(addr)

}, {LogOutput: process.stderr})
