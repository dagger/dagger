import { connect, Client } from "@dagger.io/dagger"
import { v4 as uuidv4 } from "uuid";

// initialize Dagger client
connect(async (client: Client) => {
  const project = client.git("https://github.com/dagger/dagger").branch("main").tree()

  const build = client.container()
  .from("golang:1.20")
  .withDirectory("/src", project)
  .withWorkdir("/src")
  .withExec(["go", "build", "./cmd/dagger"])

  const prodImage = client.container()
  .from("cgr.dev/chainguard/wolfi-base:latest")
  .withFile("/bin/dagger", build.file("/src/dagger"))
  .withEntrypoint(["/bin/dagger"])

  const id = uuidv4()
  const tag = `ttl.sh/dagger-${id}:1h`

  await prodImage.publish(tag)

}, {LogOutput: process.stdout})
