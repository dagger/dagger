import os
import sys

import anyio

import dagger


async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # read secret from host variable
        secret = client.set_secret("gh-secret", os.environ["GH_SECRET"])

        # use secret in container environment
        out = await (
            client.container(platform=dagger.Platform("linux/amd64"))
            .from_("alpine:3.17")
            .with_secret_variable("GITHUB_API_TOKEN", secret)
            .with_exec(["apk", "add", "curl"])
            .with_exec(
                [
                    "sh",
                    "-c",
                    """curl "https://api.github.com/repos/dagger/dagger/issues" --header "Accept: application/vnd.github+json" --header "Authorization: Bearer $GITHUB_API_TOKEN" """,
                ]
            )
            .stdout()
        )

    print(out)


anyio.run(main)
