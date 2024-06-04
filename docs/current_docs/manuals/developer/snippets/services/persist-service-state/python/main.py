from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type


@object_type
class MyModule:
    @function
    def redis(self) -> dagger.Container:
        """Create Redis service and client"""
        redis_srv = (
            dag.container()
            .from_("redis")
            .with_exposed_port(6379)
            .with_mounted_cache("/data", dag.cache_volume("my-redis"))
            .with_workdir("/data")
            .as_service()
        )

        # create Redis client container
        redis_cli = (
            dag.container()
            .from_("redis")
            .with_service_binding("redis-srv", redis_srv)
            .with_entrypoint(["redis-cli", "-h", "redis-srv"])
        )

        return redis_cli

    @function
    async def set(
        self,
        key: Annotated[str, Doc("The cache key to set")],
        value: Annotated[str, Doc("The cache value to set")],
    ) -> str:
        """Set key and value in Redis service"""
        return await self.redis().with_exec(["set", key, value]).with_exec(["save"]).stdout()

    @function
    async def get(
        self,
        key: Annotated[str, Doc("The cache key to get")],
    ) -> str:
        """Get value from Redis service"""
        return await self.redis().with_exec(["get", key]).stdout()
