import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def github_api(self, dir: dagger.Directory, secret: dagger.Secret) -> dagger.Container:
        secret_name = await secret.name()
        return dir.docker_build(
            dockerfile="Dockerfile",
            build_args=dagger.DockerBuildArgs(name="gh-secret", value=secret_name),
            secrets=[secret],
        )
