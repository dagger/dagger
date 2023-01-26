import { connect } from "@dagger.io/dagger"

connect(async (client) => {

  // use a node:16-slim container
  // mount the source code directory on the host
  // at /src in the container
  const source = client.container()
    .from("node:16-slim")
    .withMountedDirectory('/src', client.host().directory('.', { exclude: ["node_modules/", "ci/"] }))

  // set the working directory in the container
  // install application dependencies
  const runner = source
    .withWorkdir("/src")
    .withExec(["npm", "install"])

  // run application tests
  const test = runner
    .withExec(["npm", "test", "--", "--watchAll=false"])

  // build application
  // write the build output to the host
  await test
    .withExec(["npm", "run", "build"])
    .directory("./build")
    .export("./build")

  // highlight-start
  // use an nginx:alpine container
  // copy the build/ directory into the container filesystem
  // at the nginx server root
  // publish the resulting container to a registry
  const imageRef = await client.container()
    .from("nginx:alpine")
    .withDirectory('/usr/share/nginx/html', client.host().directory('./build'))
    .publish('ttl.sh/hello-dagger-' + Math.floor(Math.random() * 10000000))
  console.log(`Published image to: ${imageRef}`)
  // highlight-end

}, { LogOutput: process.stdout })
