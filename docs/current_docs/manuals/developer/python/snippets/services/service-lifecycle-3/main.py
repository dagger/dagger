from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def redis_service(self) -> str:
        # create Redis service container
        redis_srv = dag.container().from_("redis").with_exposed_port(6379).as_service()

        # create Redis client container
        redis_cli = (
            dag.container()
            .from_("redis")
            .with_service_binding("redis-srv", redis_srv)
            .with_entrypoint(["redis-cli", "-h", "redis-srv"])
        )

        # set and get value
        return (
            await redis_cli.with_exec(["set", "foo", "abc"])
            .with_exec(["get", "foo"])
            .stdout()
        )
