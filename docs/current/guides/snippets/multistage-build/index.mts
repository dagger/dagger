import Client, { connect } from "@dagger.io/dagger"

connect(async (client: Client) => {

  const project = client.host().directory(".");

  // build app
  const builder = client.container()
    .from("golang:latest")
    .withDirectory("/src", project)
    .withWorkdir("/src")
    .withEnvVariable("CGO_ENABLED", "0")
    .withExec(["go", "build", "-o", "myapp"]);

  // publish app on alpine base
  const prodImage = client.container()
    .from("alpine")
    .withFile("/bin/myapp", builder.file("/src/myapp"))
    .withEntrypoint(["/bin/myapp"]);
  const addr = await prodImage.publish("localhost:5000/multistage")

  console.log(addr)

}, {LogOutput: process.stderr, Workdir: "."})