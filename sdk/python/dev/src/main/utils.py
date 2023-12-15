from typing import Annotated

import dagger
from dagger import Doc, dag, function

from .consts import IMAGE, PYTHON_VERSION


@function
def debug_host() -> dagger.Container:
    """Container for debugging the files mounted from the host."""
    return python_base().with_(mounted_workdir(from_host()))


@function
def python_base(
    version: Annotated[str, Doc("Python version")] = PYTHON_VERSION,
) -> dagger.Container:
    """Base Python container with an activated virtual environment."""
    return (
        dag.container()
        .from_(f"python:{version}-{IMAGE}")
        .with_default_args(["/bin/bash"])  # for dagger shell
        .with_(cache("/root/.cache/pip", keys=["pip", version, IMAGE]))
        .with_(venv)
    )


def cache(
    path: Annotated[
        str,
        Doc("Location of the cache directory in the container"),
    ],
    *,
    keys: Annotated[
        list[str],
        Doc("Tags to add to the cache key"),
    ],
    init: Annotated[
        list[str] | None,
        Doc("Command to run to initialize the volume"),
    ] = None,
):
    """Add a cache volume to a container, with a module scoped cache key suffix."""

    def with_cache(ctr: dagger.Container) -> dagger.Container:
        src = None
        if init is not None:
            src = ctr.with_exec(init).directory(path)
        return ctr.with_mounted_cache(
            path,
            dag.cache_volume("-".join([*keys, "pythonsdkdev", "cache"])),
            source=src,
        )

    return with_cache


def venv(ctr: dagger.Container) -> dagger.Container:
    """Add a virtual environment to a container."""
    path = "/opt/venv"
    return (
        ctr.with_(
            cache(
                path,
                keys=["venv", PYTHON_VERSION, IMAGE],
                init=["python", "-m", "venv", path],
            )
        )
        .with_env_variable("VIRTUAL_ENV", path)
        .with_env_variable("PATH", "$VIRTUAL_ENV/bin:$PATH", expand=True)
    )


def sdk(ctr: dagger.Container) -> dagger.Container:
    """Mount and install the SDK into a container."""
    return ctr.with_mounted_directory("/sdk", dag.host().directory("/sdk")).with_exec(
        ["pip", "install", "/sdk"]
    )


def mounted_workdir(src: dagger.Directory):
    """Add directory as a mount on a container, under `/work`."""

    def _workdir(ctr: dagger.Container) -> dagger.Container:
        return ctr.with_mounted_directory("/work", src).with_workdir("/work")

    return _workdir


def requirements(file: dagger.File):
    """Install a requirements.txt file into a container."""

    def _requirements(ctr: dagger.Container) -> dagger.Container:
        return ctr.with_mounted_file("/requirements.txt", file).with_exec(
            ["pip", "install", "-r", "/requirements.txt"]
        )

    return _requirements


def from_host(
    include: list[str] | None = None,
    exclude: list[str] | None = None,
) -> dagger.Directory:
    """Get directory from host, with filtered files."""
    if exclude is None:
        exclude = []
    return dag.host().directory(
        "/src",
        include=include,
        exclude=[
            *exclude,
            # This seems to be added by the runtime container,
            # when loading the module.
            "dev/build",
            "**/*.egg-info*",
        ],
    )


def from_host_dir(path: str) -> dagger.Directory:
    """Get a sub directory from the host."""
    return from_host([path]).directory(path)


def from_host_file(path: str) -> dagger.File:
    """Get a file from the host."""
    return from_host([path]).file(path)


def from_host_req(env: str) -> dagger.File:
    """Get the requirements.txt of a specific environment from the host."""
    return from_host_file(f"requirements/{env}.txt")
