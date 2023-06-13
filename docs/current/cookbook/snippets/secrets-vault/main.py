import os
import sys

import anyio
import hvac

import dagger


async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # get secret from Vault
        secretPlaintext = await get_vault_secret(
            "MOUNT-PATH", "SECRET-ID", "SECRET-KEY"
        )

        # load secret into Dagger
        secret = client.set_secret("ghApiToken", secretPlaintext)

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


async def get_vault_secret(mount_path, secret_id, secret_key):
    # check for required variables in host environment
    for var in ["VAULT_ADDRESS", "VAULT_NAMESPACE", "VAULT_ROLE_ID", "VAULT_SECRET_ID"]:
        if var not in os.environ:
            raise OSError('"%s" environment variable must be set' % var)

    # create Vault client
    client = hvac.Client(
        url=os.environ.get("VAULT_ADDRESS"), namespace=os.environ.get("VAULT_NAMESPACE")
    )

    # log in to Vault
    client.auth.approle.login(
        role_id=os.environ.get("VAULT_ROLE_ID"),
        secret_id=os.environ.get("VAULT_SECRET_ID"),
        use_token=True,
    )

    # read and return secret
    read_response = client.secrets.kv.read_secret_version(
        path=secret_id, mount_point=mount_path, raise_on_deleted_version=True
    )
    return read_response["data"]["data"][secret_key]


anyio.run(main)
