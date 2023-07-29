import os
import sys

import anyio

import dagger

GPG_KEY = os.environ.get("GPG_KEY", "public")


async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        await (
            client.container()
            .from_("alpine:3.17")
            .with_exec(["apk", "add", "--no-cache", "gnupg"])
            .with_(await mounted_secret_directory(client, "/root/.gnupg", "~/.gnupg"))
            .with_workdir("/root")
            .with_mounted_file("myapp", client.host().file("myapp"))
            .with_exec(["gpg", "--detach-sign", "--armor", "-u", GPG_KEY, "myapp"])
            .export("myapp.asc")
        )


async def mounted_secret_directory(client: dagger.Client, target_path: str, source_path: str):
    target = anyio.Path(target_path)
    base = await anyio.Path(source_path).expanduser()
    files = [path async for path in base.rglob("*") if await path.is_file()]

    def _mounted_secret_directory(ctr: dagger.Container) -> dagger.Container:
        for path in files:
            relative = path.relative_to(base)
            secret = client.host().set_secret_file(str(relative), str(path))
            ctr = ctr.with_mounted_secret(str(target / relative), secret)

        # Fix directory permissions.
        return ctr.with_exec(
            ["sh", "-c", f"find {target} -type d -exec chmod 700 {{}} \\;"]
        )

    return _mounted_secret_directory


anyio.run(main)
