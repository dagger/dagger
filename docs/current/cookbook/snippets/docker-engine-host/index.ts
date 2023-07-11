import Client, { connect } from "@dagger.io/dagger"

connect(
  // create Dagger client
  async (client: Client) => {
    // setup container with docker socket
    const ctr = client
      .container()
      .from("docker")
      .withUnixSocket(
        "/var/run/docker.sock",
        client.host().unix_socket("/var/run/docker.sock")
      )
      .withExec(["docker", "run", "--rm", "alpine", "uname", "-a"])
      .stdout()

    // print docker run
    console.log(await ctr.stdout())
  },
  { LogOutput: process.stderr }
)
