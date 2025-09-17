import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def build(self, source: dagger.Directory) -> dagger.Container:
        """Return container image with application source code and dependencies"""
        return (
            dag.container()
            .from_("php:8.2")
            .with_exec(["apt-get", "update"])
            .with_exec(["apt-get", "install", "--yes", "git-core", "zip", "curl"])
            .with_exec(
                [
                    "sh",
                    "-c",
                    (
                        "curl -sS https://getcomposer.org/installer"
                        " | php -- --install-dir=/usr/local/bin --filename=composer"
                    ),
                ]
            )
            .with_directory(
                "/var/www",
                source.without_directory("dagger"),
            )
            .with_workdir("/var/www")
            .with_exec(["chmod", "-R", "775", "/var/www"])
            .with_env_variable("PATH", "./vendor/bin:$PATH", expand=True)
            .with_exec(["composer", "install"])
        )

    @function
    async def test(self, source: dagger.Directory) -> str:
        """Return result of unit tests"""
        return await self.build(source).with_exec(["phpunit"]).stdout()

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
        """Return address of published container image"""
        return await (
            self.build(source)
            .with_label("org.opencontainers.image.title", "PHP with Dagger")
            .with_label("org.opencontainers.image.version", version)
            .with_entrypoint(["php", "-S", "0.0.0.0:8080", "-t", "public"])
            .with_exposed_port(8080)
            .with_registry_auth(registry_address, registry_username, registry_password)
            .publish(f"{registry_address}/{registry_username}/{image_name}")
        )
