from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def redis_service(self) -> str:
        """Creates Redis service and client."""
        redis_srv = dag.container().from_("redis").with_exposed_port(6379).as_service()

        # create Redis client container
        redis_cli = (
            dag.container()
            .from_("redis")
            .with_service_binding("redis-srv", redis_srv)
            .with_entrypoint(["redis-cli", "-h", "redis-srv"])
        )

        # send ping from client to server
        return await redis_cli.with_exec(["ping"]).stdout()
