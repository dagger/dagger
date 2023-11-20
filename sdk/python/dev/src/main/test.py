import platform

import dagger
from dagger.mod import Annotated, Doc, field, function, object_type

from .consts import PYTHON_VERSION
from .utils import mounted_workdir, python_base, requirements


@object_type
class Test:
    """Run the test suite."""

    requirements: dagger.File = field()
    src: dagger.Directory = field()
    version: str = field(default=PYTHON_VERSION)

    @function
    def base(self) -> dagger.Container:
        """Base container for running tests."""
        return (
            python_base(self.version)
            .with_(requirements(self.requirements))
            .with_(mounted_workdir(self.src))
            .with_exec(["pip", "install", "-e", "."])
        )

    @function
    def pytest(self, args: list[str]) -> dagger.Container:
        """Run unit tests."""
        return (
            self.base()
            .pipeline(f"Python {self.version}")
            .with_focus()
            .with_exec(
                ["pytest", *args],
                experimental_privileged_nesting=True,
            )
        )

    @function
    def unit(self) -> dagger.Container:
        """Run unit tests."""
        return self.pytest(["-m", "not slow and not provision"])

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
        uname = platform.uname()
        os_name = uname.system.lower()
        arch_name = uname.machine.lower()
        archive_name = f"dagger_v0.x.y_{os_name}_{arch_name}.tar.gz"
        checksums_name = "checksums.txt"

        http_server = (
            python_base(self.version)
            .with_mounted_file("/src/dagger", cli_bin)
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

        ctr = dagger.dockerd().attach(
            (
                self.base()
                .pipeline(f"Python {self.version}")
                .with_mounted_file(
                    "/opt/docker.tgz",
                    dagger.http(
                        "https://download.docker.com/linux/static/stable"
                        f"/{arch_name}/docker-{docker_version}.tgz"
                    ),
                    owner="root",
                )
                .with_exec(
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

    @function
    def default(self) -> dagger.Container:
        """Run integration tests."""
        return self.pytest(["-W", "default", "-m", "not provision"])
