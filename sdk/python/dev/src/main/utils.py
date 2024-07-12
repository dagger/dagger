import os
from typing import Final

import dagger
from dagger import dag
from main.consts import PYTHON_VERSION


BASE_IMAGES: Final = {
    "3.10": "python:3.10-slim@sha256:64157e9ca781b9d18e4d7e613f4a3f19365a26d82da87ff1aa82a03eacb34687",
    "3.11": "python:3.11-slim@sha256:dad770592ab3582ab2dabcf0e18a863df9d86bd9d23efcfa614110ce49ac20e4",
    "3.12": "python:3.12-slim@sha256:541d45d3d675fb8197f534525a671e2f8d66c882b89491f9dda271f4f94dcd06",
}


def python_base(version: str = PYTHON_VERSION) -> dagger.Container:
    # TODO: move some of this to a reference module
    image_ref = BASE_IMAGES.get(version, f"python:{version}-slim")
    image_tag = image_ref.split("@")[0]
    uv_image_ref = os.getenv("DAGGER_UV_IMAGE", "ghcr.io/astral-sh/uv:latest")
    return (
        dag.container()
        .from_(image_ref)
        .with_env_variable("PYTHONUNBUFFERED", "1")
        .with_file("/usr/local/bin/uv", dag.container().from_(uv_image_ref).file("/uv"))
        .with_mounted_cache("/root/.cache/uv", dag.cache_volume("modpython-uv"))
        .with_(venv("/opt/venv"))
    )


def venv(path: str):
    def _venv(ctr: dagger.Container) -> dagger.Container:
        return (
            ctr.with_exec(["uv", "venv", path])
            .with_env_variable("VIRTUAL_ENV", path)
            .with_env_variable("PATH", "$VIRTUAL_ENV/bin:$PATH", expand=True)
        )
    return _venv

def mounted_workdir(src: dagger.Directory):
    """Add directory as a mount on a container, under `/work`."""

    def _workdir(ctr: dagger.Container) -> dagger.Container:
        return ctr.with_mounted_directory("/work", src).with_workdir("/work")

    return _workdir

