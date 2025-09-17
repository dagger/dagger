import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def deploy(
        self,
        project_name: str,
        service_location: str,
        image_address: str,
        service_port: int,
        credential: dagger.Secret,
    ) -> str:
        return await dag.google_cloud_run().create_service(
            project_name, service_location, image_address, service_port, credential
        )
