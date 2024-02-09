import dagger
from dagger import dag, object_type, field, function

@object_type
class MyModule:

    @function
    def build(source: dagger.Directory) -> dagger.Container:
        '''Build an image'''
        return (
            dag.container()
            .from_("node:21")
            .with_directory("/src", source)
            .with_workdir("/src")
            .with_exec(["cp", "-R", ".", "/home/node"])
            .with_workdir("/home/node")
            .with_exec(["npm", "install"])
            .with_entrypoint(["npm", "start"])
        )

    @function
    async def publish(self, source: dagger.Directory, registry: str, credential: dagger.Secret) -> str:
        '''Publish an image'''
        split = registry.split("/")
        return await (
            self.build(source)
            .with_registry_auth(split[0], "_json_key", credential)
            .publish(registry)
        )

    @function
    async def deploy(self, source: dagger.Directory, registry: str, service: str, credential: dagger.Secret) -> str:
        '''Deploy an image to Google Cloud Run'''

        json = await credential.plaintext()
        gcr_client = run_v2.ServicesAsyncClient(json)

        addr = self.publish(source, registry, credential)

        gcr_request = run_v2.UpdateServiceRequest(
            service=run_v2.Service(
                name=service,
                template=run_v2.RevisionTemplate(
                    containers=[
                        run_v2.Container(
                            image=addr,
                            ports=[
                                run_v2.ContainerPort(
                                    name="http1",
                                    container_port=1323,
                                ),
                            ],
                        ),
                    ],
                ),
            )
        )

        gcr_operation = await gcr_client.update_service(request=gcr_request)

        response = await gcr_operation.result()
