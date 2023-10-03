import dagger


# get base image
def base(client: dagger.Client):
    return client.container().from_("alpine:latest")


# run command in base image
async def version(client: dagger.Client):
    return await base(client).with_exec(["cat", "/etc/alpine-release"]).stdout()
