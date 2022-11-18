from datetime import datetime
from textwrap import dedent

import pytest

import dagger

pytestmark = [
    pytest.mark.slow,
]


def test_container_build():
    with dagger.Connection() as client:
        repo = client.git("https://github.com/dagger/dagger").tag("v0.3.0").tree()
        dagger_img = client.container().build(repo)

        out = dagger_img.exec(["version"]).stdout()

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

        out = container.exec(["ls", "/mnt"]).stdout()

        assert out == dedent(
            """\
            goodbye.txt
            hello.txt
            """
        )


def test_container_with_mounted_cache():
    with dagger.Connection() as client:
        cache_key = f"example-{datetime.now().isoformat()}"

        container = (
            client.container()
            .from_("alpine:3.16.2")
            .with_mounted_cache("/cache", client.cache_volume(cache_key))
        )

        for i in range(5):
            out = container.exec(
                [
                    "sh",
                    "-c",
                    "echo $0 >> /cache/x.txt; cat /cache/x.txt",
                    str(i),
                ]
            ).stdout()

        assert out == "0\n1\n2\n3\n4\n"
