from datetime import datetime
from textwrap import dedent

import pytest

import dagger
from dagger import dag

pytestmark = [
    pytest.mark.anyio,
    pytest.mark.slow,
]


@pytest.fixture(scope="module")
def anyio_backend():
    return "asyncio"


@pytest.fixture(autouse=True, scope="module")
async def _connection():
    async with dagger.connection(dagger.Config(retry=None)):
        yield


async def test_container():
    alpine = dag.container().from_("alpine:3.16.2")
    version = await alpine.with_exec(["cat", "/etc/alpine-release"]).stdout()

    assert version == "3.16.2\n"


async def test_git_repository():
    repo = dag.git("https://github.com/dagger/dagger").tag("v0.3.0").tree()
    readme = await repo.file("README.md").contents()

    assert readme.split("\n")[0] == "## What is Dagger?"


async def test_container_build():
    repo = dag.git("https://github.com/dagger/dagger").tag("v0.3.0").tree()
    dagger_img = dag.container().build(repo)

    out = await dagger_img.with_exec(["version"]).stdout()

    words = out.strip().split(" ")

    assert words[0] == "dagger"


async def test_input_arg():
    dockerfile = """\
    FROM alpine:3.16.2
    ARG SPAM=spam
    ENV SPAM=$SPAM
    CMD printenv
    """
    out = await (
        dag.container()
        .build(
            dag.directory().with_new_file("Dockerfile", dockerfile),
            build_args=[dagger.BuildArg("SPAM", "egg")],
        )
        .stdout()
    )
    assert "SPAM=egg" in out


async def test_optionals_in_input_fields():
    svc = dag.host().service([dagger.PortForward(8000)])
    field = svc._ctx.selections.pop()
    assert field.args == {"ports": [{"backend": 8000}]}


@pytest.mark.parametrize("val", ["spam", ""])
async def test_container_with_env_variable(val):
    out = await (
        dag.container()
        .from_("alpine:3.16.2")
        .with_env_variable("FOO", val)
        .with_exec(["sh", "-c", "echo -n $FOO"])
        .stdout()
    )
    assert out == val


async def test_container_with_mounted_directory():
    dir_ = (
        dag.directory()
        .with_new_file("hello.txt", "Hello, world!")
        .with_new_file("goodbye.txt", "Goodbye, world!")
    )

    container = (
        dag.container().from_("alpine:3.16.2").with_mounted_directory("/mnt", dir_)
    )

    out = await container.with_exec(["ls", "/mnt"]).stdout()

    assert out == dedent(
        """\
        goodbye.txt
        hello.txt
        """,
    )


async def test_container_with_mounted_cache():
    cache_key = "example-cache"
    filename = datetime.now().strftime("%Y-%m-%d-%H-%M-%S")

    container = (
        dag.container()
        .from_("alpine:3.16.2")
        .with_mounted_cache("/cache", dag.cache_volume(cache_key))
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
    dir_ = (
        dag.directory()
        .with_new_file("hello.txt", "Hello, world!")
        .with_new_file("goodbye.txt", "Goodbye, world!")
    )

    entries = await dir_.entries()

    assert entries == ["goodbye.txt", "hello.txt"]


async def test_host_directory():
    readme = await dag.host().directory(".").file("README.md").contents()
    assert "Dagger" in readme


async def test_object_sequence(tmp_path):
    # Test that a sequence of objects doesn't fail.
    # In this case, we're using Container.export's
    # platform_variants which is a Sequence[Container].
    variants = [
        dag.container(platform=dagger.Platform(platform))
        .from_("alpine:3.16.2")
        .with_exec(["uname", "-m"])
        for platform in ("linux/amd64", "linux/arm64")
    ]
    await dag.container().export(
        path=str(tmp_path / "export.tar.gz"),
        platform_variants=variants,
    )


async def test_container_with():
    def env(ctr: dagger.Container):
        return ctr.with_env_variable("FOO", "bar")

    def secret(token: str):
        def _secret(ctr: dagger.Container):
            return ctr.with_secret_variable("TOKEN", dag.set_secret("TOKEN", token))

        return _secret

    await (
        dag.container()
        .from_("alpine:3.16.2")
        .with_(env)
        .with_(secret("baz"))
        .with_exec(["sh", "-c", "test $FOO = bar && test $TOKEN = baz"])
        .sync()
    )


async def test_container_sync():
    base = dag.container().from_("alpine:3.16.2")

    # short cirtcut
    with pytest.raises(dagger.QueryError, match="foobar"):
        await base.with_exec(["foobar"]).sync()

    # chaining
    out = await (await base.with_exec(["echo", "spam"]).sync()).stdout()
    assert out == "spam\n"


async def test_container_awaitable():
    base = dag.container().from_("alpine:3.16.2")

    # short cirtcut
    with pytest.raises(dagger.QueryError, match="foobar"):
        await base.with_exec(["foobar"])

    # chaining
    out = await (await base.with_exec(["echo", "spam"])).stdout()
    assert out == "spam\n"


async def test_directory_sync():
    # This feature is tested in core, we're just testing if
    # sync in different types work.
    base = dag.directory().with_new_file("foo", "bar")

    # short cirtcut
    with pytest.raises(dagger.QueryError, match="no such file or directory"):
        await base.directory("foobar").sync()

    # chaining
    entries = await (await base.sync()).entries()
    assert entries == ["foo"]


async def test_return_list_of_objects():
    envs = await dag.container().from_("alpine:3.16.2").env_variables()
    assert await envs[0].name() == "PATH"
