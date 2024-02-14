import { connect } from "@dagger.io/dagger"

// initialize Dagger client
connect(
  async (client) => {
    // use NGINX container
    // add new webserver index page
    const ctr = client
      .container({ platform: "linux/amd64" })
      .from("nginx:1.23-alpine")
      .withNewFile("/usr/share/nginx/html/index.html", {
        contents: "Hello from Dagger!",
        permissions: 0o400,
      })

    // publish to local registry
    const result = await ctr.publish("127.0.0.1:5000/my-nginx:1.0")

    // print result
    console.log(`Published at: ${result}`)
  },
  { LogOutput: process.stderr },
)
