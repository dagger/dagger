import Client, { connect, Container } from "@dagger.io/dagger"

// create Dagger client
connect(async (client: Client) => {
    // setup container and
    // define environment variables
    const ctr = client
        .container()
        .from("docker")
        .withUnixSocket("/var/run/docker.sock", client.host().file("/var/run/docker.sock"))
        .withExec(["docker", "run", "--rm", "alpine", "uname", "-a"])

    console.log(await ctr.stdout())
}, {})
