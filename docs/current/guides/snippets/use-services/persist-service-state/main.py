import sys

import anyio
import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # create Redis service container
        redisSrv = (
            client.container()
            .from_("redis")
            .with_exposed_port(6379)
            .with_mounted_cache("/data", client.cache_volume("my-redis"))
            .with_workdir("/data")
            .with_exec([])
        )

        # create Redis client container
        redisCli = (
            client.container()
            .from_("redis")
            .with_service_binding("redis-srv", redisSrv)
            .with_entrypoint(["redis-cli", "-h", "redis-srv"])
        )

        # set and save value
        await redisCli.with_exec(["set", "foo", "abc"]).with_exec(["save"]).stdout()

        # get value
        val = await redisCli.with_exec(["get", "foo"]).stdout()

    print(val)


anyio.run(main)
