import { connect, Client } from "@dagger.io/dagger"

connect(
  async (client: Client) => {
    const dockerd = await client
      .container()
      .from("docker:dind")
      .asService()
      .start()

    // dockerd is now running, and will stay running
    // so you don't have to worry about it restarting after a 10 second gap

    // then in all of your tests, continue to use an explicit binding:
    const test = await client
      .container()
      .from("golang")
      .withServiceBinding("docker", dockerd)
      .withEnvVariable("DOCKER_HOST", "tcp://docker:2375")
      .withExec(["go", "test", "./..."])
      .sync()

    console.log("test: ", test)

    // or, if you prefer
    // trust `endpoint()` to construct the address
    //
    // note that this has the exact same non-cache-busting semantics as withServiceBinding,
    // since hostnames are stable and content-addressed
    //
    // this could be part of the global test suite setup.
    const dockerHost = await dockerd.endpoint({ scheme: "tcp" })

    const testWithEndpoint = await client
      .container()
      .from("golang")
      .withEnvVariable("DOCKER_HOST", dockerHost)
      .withExec(["go", "test", "./..."])
      .sync()

    console.log("testWithEndpoint: ", testWithEndpoint)

    // service.stop() is available to explicitly stop the service if needed
  },
  { LogOutput: process.stderr },
)
