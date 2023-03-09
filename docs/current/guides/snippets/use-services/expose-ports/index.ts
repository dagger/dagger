import Client, { connect } from '@dagger.io/dagger';

connect(
  async (client: Client) => {

    // create HTTP service container with exposed port 8080
    const httpSrv = client
    .container()
    .from("python")
    .withDirectory("/srv", client.directory().withNewFile("index.html", "Hello, world!"))
    .withWorkdir("/srv")
    .withExec(["python", "-m", "http.server", "8080"])
    .withExposedPort(8080)

    // get HTTP endpoint
    let val = await httpSrv.endpoint();
    console.log(val);

    val = await httpSrv.endpoint({ scheme: "http" });
    console.log(val);
  },
  { LogOutput: process.stdout }
);
