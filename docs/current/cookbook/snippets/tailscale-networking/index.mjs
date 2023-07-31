import { connect } from "@dagger.io/dagger"

// create Dagger client
connect(
  async (client) => {

    // create Tailscale authentication key as secret
    const authKeySecret = client.set_secret("tailscaleAuthkey", "TS-KEY")

    // create Tailscale service container
    tailscale = client
      .container()
      .from("tailscale/tailscale:stable")
      .withSecretVariable(name="TAILSCALE_AUTHKEY", secret=authKeySecret)
      .withExec(["/bin/sh", "-c", "tailscaled --tun=userspace-networking --socks5-server=0.0.0.0:1055 --outbound-http-proxy-listen=0.0.0.0:1055 & tailscale up --authkey $TAILSCALE_AUTHKEY &"])
      .withExposedPort(1055)

    // access Tailscale network
    http = client
      .container()
      .from("alpine:3.17")
      .withExec(["apk", "add", "curl"])
      .withServiceBinding("tailscale", tailscale)
      .withEnvVariable("ALL_PROXY", "socks5://tailscale:1055/")
      .withExec(["curl https://my.url.only.accessible.on.tailscale.network.com"])

    console.log(http.sync())
  },
  { LogOutput: process.stderr }
)
