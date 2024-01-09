import { connect, Client } from "@dagger.io/dagger"

// create Dagger client
connect(
  async (client: Client) => {
    // setup container with docker socket
    const ctr = client
      .container()
      .from("docker")
      .withUnixSocket(
        "/var/run/docker.sock",
        client.host().unixSocket("/var/run/docker.sock")
      )
      .withExec(["docker", "run", "--rm", "alpine", "uname", "-a"])
      .stdout()

    // print docker run
    console.log(await ctr.stdout())
  },
  { LogOutput: process.stderr }
)
