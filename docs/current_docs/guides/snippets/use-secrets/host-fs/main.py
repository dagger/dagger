import sys

import anyio

import dagger


async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # read file
        config = await anyio.Path("/home/USER/.config/gh/hosts.yml").read_text()

        # set secret to file contents
        secret = client.set_secret("ghConfig", config)

        # mount secret as file in container
        out = await (
            client.container(platform=dagger.Platform("linux/amd64"))
            .from_("alpine:3.17")
            .with_exec(["apk", "add", "github-cli"])
            .with_mounted_secret("/root/.config/gh/hosts.yml", secret)
            .with_workdir("/root")
            .with_exec(["gh", "auth", "status"])
            .stdout()
        )

    print(out)


anyio.run(main)
