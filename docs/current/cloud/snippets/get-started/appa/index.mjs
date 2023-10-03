import { connect } from "@dagger.io/dagger"

connect(
  async (client) => {
    const source = client
      .container()
      .from("node:16-slim")
      .withDirectory(
        "/src",
        client.host().directory(".", { exclude: ["node_modules/", "ci/"] })
      )

    const runner = source.withWorkdir("/src").withExec(["npm", "install"])

    const test = runner.withExec(["npm", "test", "--", "--watchAll=false"])

    await test
      .withExec(["npm", "run", "build"])
      .directory("./build")
      .export("./build")

    const imageRef = await client
      .container()
      .from("nginx:1.23-alpine")
      .withDirectory(
        "/usr/share/nginx/html",
        client.host().directory("./build")
      )
      .publish("ttl.sh/hello-dagger-" + Math.floor(Math.random() * 10000000))
    console.log(`Published image to: ${imageRef}`)
  },
  { LogOutput: process.stdout }
)
