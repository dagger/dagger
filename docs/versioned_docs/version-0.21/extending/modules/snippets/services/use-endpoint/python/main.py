from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def get(self) -> str:
        # start NGINX service
        service = dag.container().from_("nginx").with_exposed_port(80).as_service()
        await service.start()

        # wait for service endpoint
        endpoint = await service.endpoint(port=80, scheme="http")

        # s end HTTP request to service endpoint
        return await dag.http(endpoint).contents()
