import dagger
from dagger import dag, object_type, field, function

@object_type
class MyModule:

    source: dagger.Directory = field()

    @function
    def build(self) -> dagger.Container:
        '''Build an image'''
        return (
            dag.container()
            .from_("node:21")
            .with_directory("/home/node", self.source)
            .with_workdir("/home/node")
            .with_exec(["npm", "install"])
            .with_entrypoint(["npm", "start"])
        )

    @function
    async def publish(self, project: str, location: str, repository: str, credential: dagger.Secret) -> str:
        '''Publish an image'''
        registry = f"{location}-docker.pkg.dev/{project}/{repository}"
        return await (
            self.build()
            .with_registry_auth(f"{location}-docker.pkg.dev", "_json_key", credential)
            .publish(registry)
        )

    @function
    async def deploy(self, project: str, registry_location: str, repository: str, service_location: str, service: str, credential: dagger.Secret) -> str:
        '''Deploy an image to Google Cloud Run'''

        addr = await self.publish(project, registry_location, repository, credential)

        return await dag.google_cloud_run().update_service(project, service_location, service, addr, 3000, credential)
