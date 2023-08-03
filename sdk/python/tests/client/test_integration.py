import uuid
from datetime import datetime
from textwrap import dedent

import pytest

import dagger

pytestmark = [
    pytest.mark.anyio,
    pytest.mark.slow,
]


async def test_container():
    async with dagger.Connection() as client:
        alpine = client.container().from_("alpine:3.16.2")
        version = await alpine.with_exec(["cat", "/etc/alpine-release"]).stdout()

        assert version == "3.16.2\n"


async def test_git_repository():
    async with dagger.Connection() as client:
        repo = client.git("https://github.com/dagger/dagger").tag("v0.3.0").tree()
        readme = await repo.file("README.md").contents()

        assert readme.split("\n")[0] == "## What is Dagger?"


async def test_container_build():
    async with dagger.Connection() as client:
        repo = client.git("https://github.com/dagger/dagger").tag("v0.3.0").tree()
        dagger_img = client.container().build(repo)

        out = await dagger_img.with_exec(["version"]).stdout()

        words = out.strip().split(" ")

        assert words[0] == "dagger"


async def test_container_build_args():
    dockerfile = """\
    FROM alpine:3.16.2
    ARG SPAM=spam
    ENV SPAM=$SPAM
    CMD printenv
    """
    async with dagger.Connection() as client:
        out = await (
            client.container()
            .build(
                client.directory().with_new_file("Dockerfile", dockerfile),
                build_args=[dagger.BuildArg("SPAM", "egg")],
            )
            .stdout()
        )
        assert "SPAM=egg" in out


@pytest.mark.parametrize("val", ["spam", ""])
async def test_container_with_env_variable(val):
    async with dagger.Connection() as client:
        out = await (
            client.container()
            .from_("alpine:3.16.2")
            .with_env_variable("FOO", val)
            .with_exec(["sh", "-c", "echo -n $FOO"])
            .stdout()
        )
        assert out == val


async def test_container_with_mounted_directory():
    async with dagger.Connection() as client:
        dir_ = (
            client.directory()
            .with_new_file("hello.txt", "Hello, world!")
            .with_new_file("goodbye.txt", "Goodbye, world!")
        )

        container = (
            client.container()
            .from_("alpine:3.16.2")
            .with_mounted_directory("/mnt", dir_)
        )

        out = await container.with_exec(["ls", "/mnt"]).stdout()

        assert out == dedent(
            """\
            goodbye.txt
            hello.txt
            """,
        )


async def test_container_with_mounted_cache():
    async with dagger.Connection() as client:
        cache_key = "example-cache"
        filename = datetime.now().strftime("%Y-%m-%d-%H-%M-%S")

        container = (
            client.container()
            .from_("alpine:3.16.2")
            .with_mounted_cache("/cache", client.cache_volume(cache_key))
        )

        out = ""
        for i in range(5):
            out = await container.with_exec(
                [
                    "sh",
                    "-c",
                    f"echo $0 >> /cache/{filename}.txt; cat /cache/{filename}.txt",
                    str(i),
                ],
            ).stdout()

        assert out == "0\n1\n2\n3\n4\n"


async def test_directory():
    async with dagger.Connection() as client:
        dir_ = (
            client.directory()
            .with_new_file("hello.txt", "Hello, world!")
            .with_new_file("goodbye.txt", "Goodbye, world!")
        )

        entries = await dir_.entries()

        assert entries == ["goodbye.txt", "hello.txt"]


async def test_host_directory():
    async with dagger.Connection() as client:
        readme = await client.host().directory(".").file("README.md").contents()
        assert "Dagger" in readme


async def test_execute_timeout():
    async with dagger.Connection(dagger.Config(execute_timeout=0.5)) as client:
        alpine = client.container().from_("alpine:3.16.2")
        with pytest.raises(dagger.ExecuteTimeoutError):
            await (
                alpine.with_env_variable("_NO_CACHE", str(uuid.uuid4()))
                .with_exec(["sleep", "2"])
                .stdout()
            )


async def test_object_sequence(tmp_path):
    # Test that a sequence of objects doesn't fail.
    # In this case, we're using Container.export's
    # platform_variants which is a Sequence[Container].
    async with dagger.Connection() as client:
        variants = [
            client.container(platform=dagger.Platform(platform))
            .from_("alpine:3.16.2")
            .with_exec(["uname", "-m"])
            for platform in ("linux/amd64", "linux/arm64")
        ]
        await client.container().export(
            path=str(tmp_path / "export.tar.gz"),
            platform_variants=variants,
        )


async def test_connection_closed_error():
    async with dagger.Connection() as client:
        ...
    with pytest.raises(
        dagger.TransportError, match="Connection to engine has been closed"
    ):
        await client.container().id()


async def test_container_with():
    def env(ctr: dagger.Container):
        return ctr.with_env_variable("FOO", "bar")

    def secret(token: str, client: dagger.Client):
        def _secret(ctr: dagger.Container):
            return ctr.with_secret_variable("TOKEN", client.set_secret("TOKEN", token))

        return _secret

    async with dagger.Connection() as client:
        await (
            client.container()
            .from_("alpine:3.16.2")
            .with_(env)
            .with_(secret("baz", client))
            .with_exec(["sh", "-c", "test $FOO = bar && test $TOKEN = baz"])
            .sync()
        )


async def test_container_sync():
    async with dagger.Connection() as client:
        base = client.container().from_("alpine:3.16.2")

        # short cirtcut
        with pytest.raises(dagger.QueryError, match="foobar"):
            await base.with_exec(["foobar"]).sync()

        # chaining
        out = await (await base.with_exec(["echo", "spam"]).sync()).stdout()
        assert out == "spam\n"


async def test_container_awaitable():
    async with dagger.Connection() as client:
        base = client.container().from_("alpine:3.16.2")

        # short cirtcut
        with pytest.raises(dagger.QueryError, match="foobar"):
            await base.with_exec(["foobar"])

        # chaining
        out = await (await base.with_exec(["echo", "spam"])).stdout()
        assert out == "spam\n"


async def test_directory_sync():
    # This feature is tested in core, we're just testing if
    # sync in different types work.
    async with dagger.Connection() as client:
        base = client.directory().with_new_file("foo", "bar")

        # short cirtcut
        with pytest.raises(dagger.QueryError, match="no such file or directory"):
            await base.directory("foobar").sync()

        # chaining
        entries = await (await base.sync()).entries()
        assert entries == ["foo"]


async def test_return_list_of_objects():
    async with dagger.Connection() as client:
        envs = await client.container().from_("alpine:3.16.2").env_variables()
        assert await envs[0].name() == "PATH"


async def test_env_variable_set(mocker):
    """Check if private properties can be set manually."""
    ctx = mocker.MagicMock()
    ctx.select.return_value = ctx
    ctx.execute = mocker.AsyncMock(return_value="BAR")

    env_var = dagger.EnvVariable(ctx)
    env_var._name = "FOO"  # noqa: SLF001
    env_var._value = "foo"  # noqa: SLF001

    ctx.select.assert_not_called()
    assert await env_var.name() == "FOO"
    assert await env_var.value() == "foo"


async def test_env_variable_empty(mocker):
    """Check if private properties don't interrupt normal flow if not set."""
    ctx = mocker.MagicMock()
    ctx.select.return_value = ctx
    ctx.execute = mocker.AsyncMock(return_value="BAR")

    env_var = dagger.EnvVariable(ctx)
    assert await env_var.name() == "BAR"
