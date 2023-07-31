import { connect } from "@dagger.io/dagger"

// create Dagger client
connect(
  async (client) => {

    // create Tailscale authentication key as secret
    const authKeySecret = client.setSecret("tailscaleAuthkey", "TS-KEY")

    // create Tailscale service container
    const tailscale = client
      .container()
      .from("tailscale/tailscale:stable")
      .withSecretVariable("TAILSCALE_AUTHKEY", authKeySecret)
      .withExec(["/bin/sh", "-c", "tailscaled --tun=userspace-networking --socks5-server=0.0.0.0:1055 --outbound-http-proxy-listen=0.0.0.0:1055 & tailscale up --authkey $TAILSCALE_AUTHKEY &"])
      .withExposedPort(1055)

    // access Tailscale network
    out = await client
      .container()
      .from("alpine:3.17")
      .withExec(["apk", "add", "curl"])
      .withServiceBinding("tailscale", tailscale)
      .withEnvVariable("ALL_PROXY", "socks5://tailscale:1055/")
      .withExec(["curl", "https://TS-NETWORK-URL"])
      .sync()

    console.log(out)
  },
  { LogOutput: process.stderr }
)
