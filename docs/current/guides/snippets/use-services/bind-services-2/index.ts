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

    // create client container with service binding
    // access HTTP service, write to file and retrieve contents
    const val = await client
      .container()
      .from("alpine")
      .withServiceBinding("www", httpSrv)
      .withExec(["wget", "http://www:8080"])
      .file("index.html")
      .contents()

    console.log(val);
  },
  { LogOutput: process.stdout }
);
