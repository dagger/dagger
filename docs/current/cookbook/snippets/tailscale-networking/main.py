import sys
import anyio

import dagger

async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:

        # create Tailscale authentication key as secret
        auth_key_secret: Secret = client.set_secret("tailscaleAuthkey", "TS-KEY")

        # create Tailscale service container
        tailscale = (
            client.container()
            .from_("tailscale/tailscale:stable")
            .with_secret_variable(name="TAILSCALE_AUTHKEY", secret=auth_key_secret)
            .with_exec(["/bin/sh", "-c", "tailscaled --tun=userspace-networking --socks5-server=0.0.0.0:1055 --outbound-http-proxy-listen=0.0.0.0:1055 & tailscale up --authkey $TAILSCALE_AUTHKEY &"])
            .with_exposed_port(1055)
        )

        # access Tailscale network
        out = await (
            client.container()
            .from_("alpine:3.17")
            .with_exec(["apk", "add", "curl"])
            .with_service_binding("tailscale", tailscale)
            .with_env_variable("ALL_PROXY", "socks5://tailscale:1055/")
            .with_exec(["curl", "https://TS-NETWORK-URL"])
            .sync()
        )
        print(out)

anyio.run(main)
