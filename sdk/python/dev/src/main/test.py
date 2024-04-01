import platform
from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type


@object_type
class Test:
    """Run the test suite."""

    container: dagger.Container

    @function
    def run(
        self,
        args: Annotated[list[str], Doc("Arguments to pass to pytest")],
    ) -> dagger.Container:
        """Run the pytest command."""
        return self.container.with_exec(
            ["pytest", *args],
            experimental_privileged_nesting=True,
        )

    @function
    def default(self) -> dagger.Container:
        """Run python tests."""
        return self.run(["-Wd", "-l", "-m", "not provision"])

    @function
    def unit(self) -> dagger.Container:
        """Run unit tests."""
        return self.run(["-m", "not slow and not provision"])

    @function
    async def provision(
        self,
        cli_bin: Annotated[
            dagger.File,
            Doc("Dagger binary to use for test"),
        ],
        runner_host: Annotated[
            str | None,
            Doc("_EXPERIMENTAL_DAGGER_RUNNER_HOST value"),
        ] = None,
    ) -> dagger.Container:
        """Test provisioning.

        This publishes a cli binary in an ephemeral http server and checks
        if the SDK can download, extract and run it.
        """
        # TODO: Most of this setup can be reused for all SDKs that need to
        # test provisioning. Move it to the common `ci` module when it's
        # created and call the SDKs just for the test run.
        uname = platform.uname()
        os_name = uname.system.lower()
        arch_name = uname.machine.lower()
        archive_name = f"dagger_v0.x.y_{os_name}_{arch_name}.tar.gz"
        checksums_name = "checksums.txt"

        http_server = (
            self.container.with_mounted_file("/src/dagger", cli_bin)
            .with_workdir("/work")
            .with_exec(["tar", "czvf", archive_name, "-C", "/src", "dagger"])
            .with_exec(
                ["sha256sum", archive_name],
                redirect_stdout=checksums_name,
            )
            .with_exec(["python", "-m", "http.server"])
            .with_exposed_port(8000)
            .as_service()
        )

        http_server_url = await http_server.endpoint(scheme="http")
        archive_url = f"{http_server_url}/{archive_name}"
        checksums_url = f"{http_server_url}/{checksums_name}"

        docker_version = "24.0.7"

        ctr = dag.dockerd().attach(
            (
                self.container.with_mounted_file(
                    "/opt/docker.tgz",
                    dag.http(
                        "https://download.docker.com/linux/static/stable" f"/{arch_name}/docker-{docker_version}.tgz"
                    ),
                    owner="root",
                ).with_exec(
                    [
                        "tar",
                        "xzvf",
                        "/opt/docker.tgz",
                        "--strip-components=1",
                        "-C",
                        "/usr/local/bin",
                        "docker/docker",
                    ]
                )
            ),
            docker_version=docker_version,
        )

        if runner_host:
            ctr = ctr.with_env_variable(
                "_EXPERIMENTAL_DAGGER_RUNNER_HOST",
                runner_host,
            )

        return (
            ctr.with_service_binding("http_server", http_server)
            .with_env_variable("_INTERNAL_DAGGER_TEST_CLI_URL", archive_url)
            .with_env_variable("_INTERNAL_DAGGER_TEST_CLI_CHECKSUMS_URL", checksums_url)
            .with_exec(
                ["pytest", "-m", "provision"],
                insecure_root_capabilities=True,
            )
        )
