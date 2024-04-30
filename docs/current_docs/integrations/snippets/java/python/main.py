"""A generated module for MyModule functions

This module has been generated via dagger init and serves as a reference to
basic module structure as you get started with Dagger.

Two functions have been pre-created. You can modify, delete, or add to them,
as needed. They demonstrate usage of arguments and return types using simple
echo and grep commands. The functions can be called from the dagger CLI or
from one of the SDKs.

The first line in this comment block is a short description line and the
rest is a long description with more detail on the module's purpose or usage,
if appropriate. All modules should have a short description.
"""

import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def build(self, source: dagger.Directory) -> dagger.File:
        return (
            dag.java()
            .with_jdk("17")
            .with_maven("3.9.5")
            .with_project(source.without_directory("dagger"))
            .maven(["package"])
            .file("target/spring-petclinic-3.2.0-SNAPSHOT.jar")
        )

    @function
    async def publish(
        self,
        source: dagger.Directory,
        version: str,
        registryAddress: str,
        registryUsername: str,
        registryPassword: dagger.Secret,
        imageName: str,
    ) -> str:
        return await (
            dag.container()
            .from_("eclipse-temurin:17-alpine")
            .with_label("org.opencontainers.image.title", "Java with Dagger")
            .with_label("org.opencontainers.image.version", version)
            .with_file("/app/spring-petclinic-3.2.0-SNAPSHOT.jar", self.build(source))
            .with_entrypoint(
                [
                    "java",
                    "-jar",
                    "/app/spring-petclinic-3.2.0-SNAPSHOT.jar",
                ]
            )
            .with_registry_auth(registryAddress, registryUsername, registryPassword)
            .publish(f"{registryAddress}/{registryUsername}/{imageName}")
        )
