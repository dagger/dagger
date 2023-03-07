import sys
import uuid

import anyio
import dagger


async def main():
    if len(sys.argv) < 3:
        raise ValueError(f"usage: {sys.argv[0]} <cache-key> <command ...>")

    key, *cmd = sys.argv[1:]

    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        redis = client.container().from_("redis")

        # create Redis service with a persistent cache
        redis_srv = (
            redis.with_exposed_port(6379)
            .with_mounted_cache("/data", client.cache_volume(key))
            .with_workdir("/data")
            .with_exec([])
        )

        # create a redis-cli container that runs against the service
        redis_cli = redis.with_service_binding("redis-srv", redis_srv).with_entrypoint(
            ["redis-cli", "-h", "redis-srv"]
        )

        # create the execution plan for the user's command
        # avoid caching via an environment variable
        redis_cmd = redis_cli.with_env_variable("AT", str(uuid.uuid4())).with_exec(cmd)

        # first: run the command and immediately save
        await redis_cmd.with_exec(["save"]).exit_code()

        # then: print the output of the (cached) command
        out = await redis_cmd.stdout()

    print(out)


try:
    anyio.run(main)
except (dagger.DaggerError, ValueError) as e:
    print(f"error: {e}", file=sys.stderr)
    sys.exit(1)
