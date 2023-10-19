import sys

import anyio

import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        dockerd = await client.container().from_("docker:dind").as_service().start()

        # dockerd is now running, and will stay running
        # so you don't have to worry about it restarting after a 10 second gap

        test = await (
            client.container()
            .from_("golang")
            .with_service_binding("docker", dockerd)
            .with_env_variable("DOCKER_HOST", "tcp://docker:2375")
            .with_exec(["go", "test", "./..."])
            .sync()
        )

        print("test: " + test)

        # or, if you prefer
        # trust `endpoint()` to construct the address
        #
        # note that this has the exact same non-cache-busting semantics as with_service_binding,
        # since hostnames are stable and content-addressed
        #
        # this could be part of the global test suite setup.

        docker_host = await dockerd.endpoint(scheme="tcp")

        test_with_endpoint = await (
            client.container()
            .from_("golang")
            .with_env_variable("DOCKER_HOST", docker_host)
            .with_exec(["go", "test", "./..."])
            .sync()
        )

        print("test_with_endpoint: " + test_with_endpoint)

        # service.stop() is available to explicitly stop the service if needed


anyio.run(main)
