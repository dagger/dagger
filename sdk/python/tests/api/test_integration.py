from datetime import datetime
from textwrap import dedent

import pytest

import dagger
from dagger.connectors.docker import Engine

pytestmark = [
    pytest.mark.anyio,
    pytest.mark.slow,
]

@pytest.fixture(scope="module")
def engine_cfg():
    cfg = dagger.Config()
    if cfg.host.scheme == "docker-image":
        with Engine(cfg) as engine:
            yield engine.cfg
    else:
        return cfg


async def test_container(engine_cfg):
    async with dagger.Connection(engine_cfg) as client:
        alpine = client.container().from_("alpine:3.16.2")
        version = await alpine.exec(["cat", "/etc/alpine-release"]).stdout().contents()

        assert version == "3.16.2\n"


async def test_git_repository(engine_cfg):
    async with dagger.Connection(engine_cfg) as client:
        repo = client.git("https://github.com/dagger/dagger").tag("v0.3.0").tree()
        readme = await repo.file("README.md").contents()

        assert readme.split("\n")[0] == "## What is Dagger?"


async def test_container_build(engine_cfg):
    async with dagger.Connection(engine_cfg) as client:
        repo_id = (
            await client.git("https://github.com/dagger/dagger")
            .tag("v0.3.0")
            .tree()
            .id()
        )

        dagger_img = client.container().build(repo_id)

        out = await dagger_img.exec(["version"]).stdout().contents()

        words = out.strip().split(" ")

        assert words[0] == "dagger"


async def test_container_with_env_variable(engine_cfg):
    async with dagger.Connection(engine_cfg) as client:
        container = (
            client.container().from_("alpine:3.16.2").with_env_variable("FOO", "bar")
        )
        out = await container.exec(["sh", "-c", "echo -n $FOO"]).stdout().contents()

        assert out == "bar"


async def test_container_with_mounted_directory(engine_cfg):
    async with dagger.Connection(engine_cfg) as client:
        dir_id = await (
            client.directory()
            .with_new_file("hello.txt", "Hello, world!")
            .with_new_file("goodbye.txt", "Goodbye, world!")
            .id()
        )

        container = (
            client.container()
            .from_("alpine:3.16.2")
            .with_mounted_directory("/mnt", dir_id)
        )

        out = await container.exec(["ls", "/mnt"]).stdout().contents()

        assert out == dedent(
            """\
            goodbye.txt
            hello.txt
            """
        )


async def test_container_with_mounted_cache(engine_cfg):
    async with dagger.Connection(engine_cfg) as client:
        cache_key = f"example-{datetime.now().isoformat()}"

        cache_id = await client.cache_volume(cache_key).id()

        container = (
            client.container()
            .from_("alpine:3.16.2")
            .with_mounted_cache("/cache", cache_id)
        )

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

        assert out == "0\n1\n2\n3\n4\n"


async def test_directory(engine_cfg):
    async with dagger.Connection(engine_cfg) as client:
        dir = (
            client.directory()
            .with_new_file("hello.txt", "Hello, world!")
            .with_new_file("goodbye.txt", "Goodbye, world!")
        )

        entries = await dir.entries()

        assert entries == ["goodbye.txt", "hello.txt"]


async def test_host_workdir(engine_cfg):
    async with dagger.Connection(engine_cfg) as client:
        readme = await client.host().workdir().file("README.md").contents()
        assert "Dagger" in readme
