from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def get(self) -> str:
        # start NGINX service
        svc = dag.container().from_("nginx").with_exposed_port(80).as_service()
        await svc.start()

        # wait for service endpoint
        ep = await svc.endpoint(port=80, scheme="http")

        # s end HTTP request to service endpoint
        return await dag.http(ep).contents()
