import contextlib

import dagger
from dagger import dag, function, object_type


@contextlib.asynccontextmanager
async def managed_service(svc: dagger.Service):
    """Start and stop a service."""
    yield await svc.start()
    await svc.stop()


@object_type
class MyModule:
    @function
    async def redis_service(self) -> str:
        """Explicitly start and stop a Redis service."""
        redis_srv = dag.container().from_("redis").with_exposed_port(6379).as_service()

        # start Redis ahead of time so it stays up for the duration of the test
        # and stop when done
        async with managed_service(redis_srv) as redis_srv:
            # create Redis client container
            redis_cli = (
                dag.container()
                .from_("redis")
                .with_service_binding("redis-srv", redis_srv)
                .with_entrypoint(["redis-cli", "-h", "redis-srv"])
            )

            # set value
            setter = await redis_cli.with_exec(["set", "foo", "abc"]).stdout()

            # get value
            getter = await redis_cli.with_exec(["get", "foo"]).stdout()

            return setter + getter
