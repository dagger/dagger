import { connect, Client } from "@dagger.io/dagger"

// initialize Dagger client
connect(
  async (client: Client) => {
    // use NGINX container
    // add new webserver index page
    const ctr = client
      .container({ platform: "linux/amd64" })
      .from("nginx:1.23-alpine")
      .withNewFile("/usr/share/nginx/html/index.html", {
        contents: "Hello from Dagger!",
        permissions: 0o400,
      })

    // export to host filesystem
    const result = await ctr.export("/tmp/my-nginx.tar")

    // print result
    console.log(`Exported image: ${result}`)
  },
  { LogOutput: process.stderr }
)
