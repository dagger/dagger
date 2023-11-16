from collections.abc import Sequence

import anyio

import dagger
from dagger.mod import Annotated, Doc, function

from .consts import DEP_ENVS
from .deps import Deps, Hatch
from .docs import Docs
from .lint import Lint
from .test import Test
from .utils import (
    from_host,
    from_host_dir,
    from_host_file,
    from_host_req,
    mounted_workdir,
    python_base,
    sdk,
)


@function
def deps(
    env: Annotated[
        str,
        Doc(f"The hatch environment to use. Can be one of {DEP_ENVS}"),
    ],
) -> Deps:
    """Manage the SDK's development dependencies."""
    return Deps(
        env=env,
        hatch_config=from_host_file("hatch.toml"),
    )


@function
def all_deps() -> list[Deps]:
    """Manage the SDK's development dependencies."""
    return [deps(env) for env in DEP_ENVS]


@function
def lock(
    src: Annotated[
        dagger.Directory | None,
        Doc("Directory with the current pinned requirements.txt files"),
    ] = None,
) -> dagger.Directory:
    """Update the the SDK's pinned development dependencies."""
    if src is None:
        src = from_host(["requirements/*.txt"]).directory("/requirements")
    dist = src
    for env in DEP_ENVS:
        dist = dist.with_file(f"{env}.txt", deps(env).lock())
    return src.diff(dist)


@function
def publish(
    version: Annotated[str, Doc("The SDK version to publish")],
    token: Annotated[dagger.Secret, Doc("The PyPI token to use")],
) -> dagger.Container:
    """Publish the SDK to PyPI."""
    hatch = Hatch()
    artifacts = hatch.build(from_host(), version.removeprefix("sdk/python/v"))
    return hatch.publish(artifacts, token)


@function(doc=Lint.__doc__)
def lint(
    src: dagger.Directory | None = None,
    requirements: dagger.File | None = None,
    pyproject: dagger.File | None = None,
) -> Lint:
    return Lint(
        src=src
        or from_host(
            [
                "**/*.py",
                "**/*ruff.toml",
                "**/pyproject.toml",
                "**/.gitignore",
            ]
        ),
        requirements=requirements or from_host_req("lint"),
        pyproject=pyproject,
    )


@function
def test(
    src: dagger.Directory | None = None,
    requirements: dagger.File | None = None,
    version: str | None = None,
) -> Test:
    """Run the test suite."""
    kwargs = {}
    if version is not None:
        kwargs["version"] = version
    return Test(
        requirements=requirements or from_host_req("test"),
        src=src or from_host(["pyproject.toml", "README.md", "src/", "tests/"]),
        **kwargs,
    )


@function
def tests(
    src: dagger.Directory | None = None,
    requirements: dagger.File | None = None,
    versions: Sequence[str] = ("3.10", "3.11"),
) -> str:
    return [test(src, requirements, version) for version in versions]


@function
def docs(
    src: dagger.Directory | None = None,
    requirements: dagger.File | None = None,
) -> Docs:
    """Reference documentation (Sphinx)."""
    return Docs(
        src=src or from_host_dir("docs/"),
        requirements=requirements or from_host_req("docs"),
    )


@function
def script(file: dagger.File) -> dagger.Container:
    """Run a Python script."""
    return (
        python_base()
        .with_(sdk)
        .with_(
            mounted_workdir(
                dagger.directory().with_file("main.py", file),
            ),
        )
        .with_focus()
        .with_exec(
            ["python", "main.py"],
            experimental_privileged_nesting=True,
        )
    )
