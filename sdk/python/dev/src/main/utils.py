import os

import dagger
from dagger import dag


def python_base() -> dagger.Container:
    # TODO: move some of this to a reference module
    image_ref = os.getenv("DAGGER_BASE_IMAGE", "python:3.11-slim")
    image_tag = image_ref.split("@")[0]
    base = dag.container().from_(image_ref).with_env_variable("PYTHONUNBUFFERED", "1")
    return (
        base.with_file(
            "/usr/local/bin/uv",
            (
                base.with_file(
                    "requirements.lock",
                    dag.current_module().source().file("tools/uv/requirements.lock"),
                )
                .with_exec(["pip", "install", "--no-cache", "-r", "requirements.lock"])
                .file("/usr/local/bin/uv")
            ),
        )
        .with_mounted_cache("/root/.cache/uv", dag.cache_volume(f"modpython-uv-{image_tag}"))
        .with_exec(["uv", "venv", "/opt/venv"])
        .with_env_variable("VIRTUAL_ENV", "/opt/venv")
        .with_env_variable("PATH", "$VIRTUAL_ENV/bin:$PATH", expand=True)
    )


def mounted_workdir(src: dagger.Directory):
    """Add directory as a mount on a container, under `/work`."""

    def _workdir(ctr: dagger.Container) -> dagger.Container:
        return ctr.with_mounted_directory("/work", src).with_workdir("/work")

    return _workdir

