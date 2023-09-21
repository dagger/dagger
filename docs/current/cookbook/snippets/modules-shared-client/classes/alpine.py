import dagger


class Alpine:
    __client: dagger.Client

    # initialize pipeline class
    def __init__(self, client: dagger.Client):
        self.__client = client

    # get base image
    def __base(self):
        return self.__client.container().from_("alpine:latest")

    # run command in base image
    async def version(self):
        return await self.__base().with_exec(["cat", "/etc/alpine-release"]).stdout()
