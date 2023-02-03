import uuid
from datetime import datetime
from textwrap import dedent

import pytest

import dagger
from dagger.api.gen_sync import Platform
from dagger.exceptions import ExecuteTimeoutError

pytestmark = [
    pytest.mark.slow,
]


def test_container_build():
    with dagger.Connection() as client:
        repo = client.git("https://github.com/dagger/dagger").tag("v0.3.0").tree()
        dagger_img = client.container().build(repo)

        out = dagger_img.with_exec(["version"]).stdout()

        words = out.strip().split(" ")

        assert words[0] == "dagger"


def test_container_with_mounted_directory():
    with dagger.Connection() as client:
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

        out = container.with_exec(["ls", "/mnt"]).stdout()

        assert out == dedent(
            """\
            goodbye.txt
            hello.txt
            """,
        )


def test_container_with_mounted_cache():
    with dagger.Connection() as client:
        cache_key = "example-cache"
        filename = f"{datetime.now().isoformat()}.txt"

        container = (
            client.container()
            .from_("alpine:3.16.2")
            .with_mounted_cache("/cache", client.cache_volume(cache_key))
        )

        for i in range(5):
            out = container.with_exec(
                [
                    "sh",
                    "-c",
                    f"echo $0 >> /cache/{filename}; cat /cache/{filename}",
                    str(i),
                ],
            ).stdout()

        assert out == "0\n1\n2\n3\n4\n"


def test_execute_timeout():
    with dagger.Connection(dagger.Config(execute_timeout=0.5)) as client:
        alpine = client.container().from_("alpine:3.16.2")
        with pytest.raises(ExecuteTimeoutError):
            (
                alpine.with_env_variable("_NO_CACHE", str(uuid.uuid4()))
                .with_exec(["sleep", "2"])
                .stdout()
            )


def test_object_sequence(tmp_path):
    # Test that a sequence of objects doesn't fail.
    # In this case, we're using Container.export's
    # platform_variants which is a Sequence[Container].
    with dagger.Connection() as client:
        variants = [
            client.container(platform=Platform(platform))
            .from_("alpine:3.16.2")
            .with_exec(["uname", "-m"])
            for platform in ("linux/amd64", "linux/arm64")
        ]
        client.container().export(
            path=str(tmp_path / "export.tar.gz"),
            platform_variants=variants,
        )
