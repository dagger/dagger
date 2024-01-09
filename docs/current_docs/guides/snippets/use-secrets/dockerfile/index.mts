import { connect, Client } from "@dagger.io/dagger"

// check for required environment variable
if (!process.env["GH_SECRET"]) {
  console.log(`GH_SECRET variable must be set`)
  process.exit()
}

// initialize Dagger client
connect(
  async (client: Client) => {
    // read secret from host variable
    const secret = client.setSecret("gh-secret", process.env["GH_SECRET"])

    // set context directory for Dockerfile build
    const contextDir = client.host().directory(".")

    // build using Dockerfile
    // specify secrets for Dockerfile build
    // secrets will be mounted at /run/secrets/[secret-name]
    const out = await contextDir
      .dockerBuild({
        dockerfile: "Dockerfile",
        secrets: [secret],
      })
      .stdout()

    // print result
    console.log(out)
  },
  { LogOutput: process.stderr }
)
