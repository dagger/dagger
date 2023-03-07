import sys

import anyio
import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # create Redis service container
        redis_srv = (
            client.container().from_("redis").with_exposed_port(6379).with_exec([])
        )

        # create Redis client container
        redis_cli = (
            client.container()
            .from_("redis")
            .with_service_binding("redis-srv", redis_srv)
            .with_entrypoint(["redis-cli", "-h", "redis-srv"])
        )

        # set value
        setter = await redis_cli.with_exec(["set", "foo", "abc"]).stdout()

        # get value
        getter = await redis_cli.with_exec(["get", "foo"]).stdout()

    print(setter)
    print(getter)


anyio.run(main)
