import os
import sys
from pathlib import Path

import anyio

import dagger


async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        gpg_files = [
            "/home/USER/.gnupg/pubring.kbx",
            "/home/USER/.gnupg/trustdb.gpg",
            "/home/USER/.gnupg/public.key",
        ]

        source_dir = "/home/USER/.gnupg/private-keys-v1.d/"
        target_dir = "/root/.gnupg/private-keys-v1.d/"

        ctr = client.container().from_("alpine:3.17")

        for file_path in gpg_files:
            secret = client.host().set_secret_file(Path.name(file_path), file_path)
            ctr = ctr.with_mounted_secret(
                f"/root/.gnupg/{Path.name(file_path)}", secret
            )

        ctr = ctr.with_(mount_directory_as_secret(client, source_dir, target_dir))

        binary_name = "binary-name"
        ctr = (
            ctr.with_exec(["apk", "add", "--no-cache", "gnupg"])
            .with_exec(["chmod", "700", "/root/.gnupg"])
            .with_exec(["chmod", "700", "/root/.gnupg/private-keys-v1.d"])
            .with_exec(["gpg", "--import", "/root/.gnupg/public.key"])
            .with_exec(
                ["gpg", "--import", "/root/.gnupg/private-keys-v1.d/private.key"]
            )
            .with_workdir("/root")
            .with_exec(["gpg", "--detach-sign", "--armor", binary_name])
            .with_exec(["ls", "-l", f"{binary_name}.asc"])
        )

        print(await ctr.stdout())


def mount_directory_as_secret(client, source_dir, target_dir):
    def mount_directory_as_secret_inner(ctr: dagger.Container):
        for root, _dirs, files in os.walk(source_dir):
            for file in files:
                full_path = Path(root) / file
                target_path = Path(target_dir) / Path(full_path).relative_to(source_dir)

                secret = client.host().set_secret_file(Path.name(full_path), full_path)
                ctr = ctr.with_mounted_secret(target_path, secret)
        return ctr

    return mount_directory_as_secret_inner


anyio.run(main)
