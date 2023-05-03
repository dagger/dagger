import sys
import anyio
import requests
import os

import dagger

async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:

        # get secret from Doppler
        secretPlaintext = await get_doppler_secret("PROJECT-ID", "CONFIG-ID", "SECRET-ID")

        # load secret into Dagger
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

async def get_doppler_secret(project_id, config_id, secret_id):

    # check for Doppler service token in host environment
    if "DOPPLER_TOKEN" not in os.environ:
        raise EnvironmentError("DOPPLER_TOKEN environment variable must be set")

    # prepare Doppler API request
    secret_uri = f"https://api.doppler.com/v3/configs/config/secret?project={project_id}&config={config_id}&name={secret_id}"
    headersAuth = {
        'Authorization': 'Bearer ' + os.environ.get("DOPPLER_TOKEN")
    }

    # read API response
    response = requests.get(secret_uri, headers=headersAuth)
    json = response.json()
    return json['value']['raw']

anyio.run(main)
