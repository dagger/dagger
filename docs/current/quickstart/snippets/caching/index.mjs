import { connect } from "@dagger.io/dagger"

connect(async (client) => {

  // highlight-start
  // create a cache volume
  const nodeCache = client.cacheVolume("node")

  // use a node:16-slim container
  // mount the source code directory on the host
  // at /src in the container
  // mount the cache volume to persist dependencies
  const source = client.container()
    .from("node:16-slim")
    .withMountedDirectory('/src', client.host().directory('.', { exclude: ["node_modules/", "ci/"] }))
    .withMountedCache("/src/node_modules", nodeCache)
  // highlight-end

  // set the working directory in the container
  // install application dependencies
  const runner = source
    .withWorkdir("/src")
    .withExec(["npm", "install"])

  // run application tests
  const test = runner
    .withExec(["npm", "test", "--", "--watchAll=false"])

  // first stage
  // build application
  const buildDir = test
    .withExec(["npm", "run", "build"])
    .directory("./build")

  // second stage
  // use an nginx:alpine container
  // copy the build/ directory from the first stage
  // publish the resulting container to a registry
  const imageRef = await client.container()
    .from("nginx:alpine")
    .withDirectory('/usr/share/nginx/html', buildDir)
    .publish('ttl.sh/hello-dagger-' + Math.floor(Math.random() * 10000000))
  console.log(`Published image to: ${imageRef}`)

}, { LogOutput: process.stdout })
