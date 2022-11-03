from datetime import datetime
from textwrap import dedent

import pytest

import dagger

pytestmark = [
    pytest.mark.anyio,
    pytest.mark.slow,
]


async def test_python_command():
    async with dagger.Connection() as client:
        alpine = client.container().from_("python:3.10.8-alpine")

        version = await alpine.exec(["python", "-V"]).stdout().contents()
        assert version.value == "Python 3.10.8\n"


async def test_container():
    async with dagger.Connection() as client:
        alpine = client.container().from_("alpine:3.16.2")
        version = await alpine.exec(["cat", "/etc/alpine-release"]).stdout().contents()

        assert version.value == "3.16.2\n"


async def test_git_repository():
    async with dagger.Connection() as client:
        repo = client.git("https://github.com/dagger/dagger").tag("v0.3.0").tree()
        readme = await repo.file("README.md").contents()

        assert readme.value.split("\n")[0] == "## What is Dagger?"


async def test_container_build():
    async with dagger.Connection() as client:
        repo = await client.git("https://github.com/dagger/dagger").tag("v0.3.0").tree().id()

        dagger_img = client.container().build(repo.value)

        out = await dagger_img.exec(["version"]).stdout().contents()

        words = out.value.strip().split(" ")

        assert words[0] == "dagger"


async def test_container_with_env_variable():
    async with dagger.Connection() as client:
        container = client.container().from_("alpine:3.16.2").with_env_variable("FOO", "bar")
        out = await container.exec(["sh", "-c", "echo -n $FOO"]).stdout().contents()

        assert out.value == "bar"


async def test_container_with_mounted_directory():
    async with dagger.Connection() as client:
        dir = await (
            client.directory()
            .with_new_file("hello.txt", "Hello, world!")
            .with_new_file("goodbye.txt", "Goodbye, world!")
            .id()
        )

        container = client.container().from_("alpine:3.16.2").with_mounted_directory("/mnt", dir.value)

        out = await container.exec(["ls", "/mnt"]).stdout().contents()

        assert out.value == dedent(
            """\
            goodbye.txt
            hello.txt
            """
        )


async def test_container_with_mounted_cache():
    async with dagger.Connection() as client:
        cache_key = f"example-{datetime.now().isoformat()}"

        cache = await client.cache_volume(cache_key).id()

        container = client.container().from_("alpine:3.16.2").with_mounted_cache("/cache", cache.value)

        for i in range(5):
            out = (
                await container.exec(
                    [
                        "sh",
                        "-c",
                        "echo $0 >> /cache/x.txt; cat /cache/x.txt",
                        str(i),
                    ]
                )
                .stdout()
                .contents()
            )

        assert out.value == "0\n1\n2\n3\n4\n"


async def test_directory():
    async with dagger.Connection() as client:
        dir = (
            client.directory()
            .with_new_file("hello.txt", "Hello, world!")
            .with_new_file("goodbye.txt", "Goodbye, world!")
        )

        entries = await dir.entries()

        assert entries.value == ["goodbye.txt", "hello.txt"]


async def test_host_workdir():
    async with dagger.Connection(dagger.Config(workdir=".")) as client:
        readme = await client.host().workdir().file("README.md").contents()
        lines = readme.value.strip().split("\n")

        assert lines[0] == "# Dagger Python SDK"
