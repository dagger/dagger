import sys
import anyio
import dagger
from google.cloud import secretmanager

async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:

        secretPlaintext = await gcp_get_secret_plaintext("PROJECT-ID", "SECRET-ID")

        # read secret from host variable
        secret = client.set_secret("ghApiToken", secretPlaintext)

        # use secret in container environment
        out = await (
            client.container(platform=dagger.Platform("linux/amd64"))
            .from_("alpine:3.17")
            .with_secret_variable("GITHUB_API_TOKEN", secret)
            .with_exec(["apk", "add", "curl"])
            .with_exec(["sh", "-c", """curl "https://api.github.com/repos/dagger/dagger/issues" --header "Accept: application/vnd.github+json" --header "Authorization: Bearer $GITHUB_API_TOKEN" """])
            .stdout()
        )

        # print result
        print(out)

anyio.run(main)

async def gcp_get_secret_plaintext(project_id, secret_id):
    client = secretmanager.SecretManagerServiceClient()

    secret_uri = f"projects/{project_id}/secrets/{secret_id}/versions/1"

    response = client.access_secret_version(request={"name": secret_uri})

    secret_plaintext = response.payload.data.decode("UTF-8")
