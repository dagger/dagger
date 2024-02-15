import { connect, Client } from "@dagger.io/dagger"
import fetch from "node-fetch"

connect(
  async (client: Client) => {
    // create HTTP service container with exposed port 8080
    const httpSrv = client
      .container()
      .from("python")
      .withDirectory(
        "/srv",
        client.directory().withNewFile("index.html", "Hello, world!"),
      )
      .withWorkdir("/srv")
      .withExec(["python", "-m", "http.server", "8080"])
      .withExposedPort(8080)
      .asService()

    // expose HTTP service to host
    const tunnel = await client.host().tunnel(httpSrv).start()

    // get HTTP service address
    const srvAddr = await tunnel.endpoint()

    // access HTTP service from host
    // print response
    await fetch("http://" + srvAddr)
      .then((res) => res.text())
      .then((body) => console.log(body))
  },
  { LogOutput: process.stderr },
)
