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
        registry_address: str,
        registry_username: str,
        registry_password: dagger.Secret,
        image_name: str,
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
            .with_registry_auth(registry_address, registry_username, registry_password)
            .publish(f"{registry_address}/{registry_username}/{image_name}")
        )
