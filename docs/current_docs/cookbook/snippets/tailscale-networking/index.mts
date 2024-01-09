import { connect, Client } from "@dagger.io/dagger"

// check for required variables in host environment
const vars = ["TAILSCALE_AUTHKEY", "TAILSCALE_SERVICE_URL"]
vars.forEach((v) => {
  if (!process.env[v]) {
    console.log(`${v} variable must be set`)
    process.exit()
  }
})

// create Dagger client
connect(
  async (client: Client) => {
    // create Tailscale authentication key as secret
    const authKeySecret = client.setSecret(
      "tailscaleAuthkey",
      process.env.TAILSCALE_AUTHKEY
    )

    const tailscaleServiceURL = process.env.TAILSCALE_SERVICE_URL

    // create Tailscale service container
    const tailscale = client
      .container()
      .from("tailscale/tailscale:stable")
      .withSecretVariable("TAILSCALE_AUTHKEY", authKeySecret)
      .withExec([
        "/bin/sh",
        "-c",
        "tailscaled --tun=userspace-networking --socks5-server=0.0.0.0:1055 --outbound-http-proxy-listen=0.0.0.0:1055 & tailscale up --authkey $TAILSCALE_AUTHKEY &",
      ])
      .withExposedPort(1055)

    // access Tailscale network
    const out = await client
      .container()
      .from("alpine:3.17")
      .withExec(["apk", "add", "curl"])
      .withServiceBinding("tailscale", tailscale)
      .withEnvVariable("ALL_PROXY", "socks5://tailscale:1055/")
      .withExec(["curl", "--silent", "--verbose", tailscaleServiceURL])
      .sync()

    console.log(out)
  },
  { LogOutput: process.stderr }
)
