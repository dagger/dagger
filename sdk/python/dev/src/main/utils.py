import dagger
from dagger.mod import function

from .consts import IMAGE, PYTHON_VERSION


@function
def debug_host() -> dagger.Container:
    return python_base().with_(mounted_workdir(from_host()))


@function
def python_base(version: str = PYTHON_VERSION) -> dagger.Container:
    return (
        dagger.container()
        .from_(f"python:{version}-{IMAGE}")
        .with_default_args(args=["/bin/bash"])  # for dagger shell
        .with_(cache("/root/.cache/pip", keys=["pip", version, IMAGE]))
        .with_(venv)
    )


def cache(path: str, *, keys: list[str], init: list[str] | None = None):
    def with_cache(ctr: dagger.Container) -> dagger.Container:
        src = None
        if init is not None:
            src = ctr.with_exec(init).directory(path)
        return ctr.with_mounted_cache(
            path,
            dagger.cache_volume("-".join([*keys, "pythonsdkdev", "cache"])),
            source=src,
        )

    return with_cache


def venv(ctr: dagger.Container) -> dagger.Container:
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
    return ctr.with_mounted_directory(
        "/sdk", dagger.host().directory("/sdk")
    ).with_exec(["pip", "install", "/sdk"])


def mounted_workdir(src: dagger.Directory):
    def _workdir(ctr: dagger.Container) -> dagger.Container:
        return ctr.with_mounted_directory("/work", src).with_workdir("/work")

    return _workdir


def requirements(file: dagger.File):
    def _requirements(ctr: dagger.Container) -> dagger.Container:
        return ctr.with_mounted_file("/requirements.txt", file).with_exec(
            ["pip", "install", "-r", "/requirements.txt"]
        )

    return _requirements


def from_host(
    include: list[str] | None = None,
    exclude: list[str] | None = None,
) -> dagger.Directory:
    if exclude is None:
        exclude = []
    return dagger.host().directory(
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
    return from_host([path]).directory(path)


def from_host_file(path: str) -> dagger.File:
    return from_host([path]).file(path)


def from_host_req(env: str) -> dagger.File:
    return from_host_file(f"requirements/{env}.txt")
