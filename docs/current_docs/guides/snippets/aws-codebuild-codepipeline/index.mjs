import { connect } from "@dagger.io/dagger"

// check for required variables in host environment
const vars = ["REGISTRY_ADDRESS", "REGISTRY_USERNAME", "REGISTRY_PASSWORD"]
vars.forEach((v) => {
  if (!process.env[v]) {
    console.log(`${v} variable must be set`)
    process.exit()
  }
})

// initialize Dagger client
connect(
  async (client) => {
    // set registry password as Dagger secret
    const secret = client.setSecret("password", process.env.REGISTRY_PASSWORD)

    // get reference to the project directory
    const source = client
      .host()
      .directory(".", { exclude: ["node_modules/", "ci/"] })

    // use a node:18-slim container
    const node = client
      .container({ platform: "linux/amd64" })
      .from("node:18-slim")

    // mount the project directory
    // at /src in the container
    // set the working directory in the container
    // install application dependencies
    // build application
    // set default arguments
    const app = node
      .withDirectory("/src", source)
      .withWorkdir("/src")
      .withExec(["npm", "install"])
      .withExec(["npm", "run", "build"])
      .withDefaultArgs(["npm", "start"])

    // publish image to registry
    // at registry path [registry-username]/myapp
    // print image address
    const address = await app
      .withRegistryAuth(
        process.env.REGISTRY_ADDRESS,
        process.env.REGISTRY_USERNAME,
        secret,
      )
      .publish(`${process.env.REGISTRY_USERNAME}/myapp`)
    console.log(`Published image to: ${address}`)
  },
  { LogOutput: process.stdout },
)
