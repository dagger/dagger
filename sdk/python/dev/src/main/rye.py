import platform
from typing import Final, Self

import dagger
from dagger import dag, field, function, object_type

RYE_HOME: Final[str] = "/opt/rye"


@object_type
class Rye:
    container: dagger.Container = field()
    version: str = ""

    @function
    def download_url(self) -> str:
        arch = platform.machine()
        name = f"rye-{arch}-linux"
        subpath = f"download/{self.version}" if self.version else "latest/download"
        return f"https://github.com/astral-sh/rye/releases/{subpath}/{name}.gz"

    @function
    def download(self) -> dagger.File:
        return (
            self.container.with_file("rye.gz", dag.http(self.download_url()))
            .with_exec(["gunzip", "-f", "rye.gz"])
            .file("rye")
        )

    @function
    def self_install(self) -> Self:
        self.container = (
            self.container.with_env_variable("RYE_HOME", RYE_HOME)
            .with_env_variable("PATH", "$RYE_HOME/shims:$PATH", expand=True)
            .with_env_variable("RYE_NO_AUTO_INSTALL", "1")
            .with_env_variable("RYE_TOOLCHAIN", "/usr/local/bin/python")
            .with_file("/usr/local/bin/rye", self.download(), permissions=0o700)
            .with_exec(["rye", "config", "--set-bool", "behavior.use-uv=true"])
            .with_exec(["rye", "config", "--set-bool", "behavior.global-python=true"])
            .with_exec(["rye", "self", "install", "--yes", "--no-modify-path"])
            # .with_exec(["rye", "install", "hatch"])
            # .with_exec(["rye", "install", "ruff"])
            # .with_exec(["rye", "install", "uv"])
        )
        return self

    @function
    def install(self, package: str) -> Self:
        self.container = self.container.with_exec(["rye", "install", package])
        return self
