import { connect, Client } from "@dagger.io/dagger"

connect(
  async (client: Client) => {
    // get host directory
    const project = client.host().directory(".")

    // build app
    const builder = client
      .container()
      .from("golang:latest")
      .withDirectory("/src", project)
      .withWorkdir("/src")
      .withEnvVariable("CGO_ENABLED", "0")
      .withExec(["go", "build", "-o", "myapp"])

    // publish binary on alpine base
    const prod = client
      .container()
      .from("alpine")
      .withFile("/bin/myapp", builder.file("/src/myapp"))
      .withEntrypoint(["/bin/myapp"])
    const addr = await prod.publish("localhost:5000/multistage")

    console.log(addr)
  },
  { LogOutput: process.stderr },
)
