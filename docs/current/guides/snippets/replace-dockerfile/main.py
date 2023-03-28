import sys

import anyio
import dagger


NPROC = "1"
GNU_ARCH = "arm64"
PUBLISH_ADDRESS = "DOCKER-HUB-USERNAME/my-memcached"


async def main():
    config = dagger.Config(log_output=sys.stderr)

    # create a Dagger client
    async with dagger.Connection(config) as client:
        # set the base container
        # set environment variables
        memcached = (
            client.container()
            .from_("alpine:3.17")
            .with_exec(["addgroup", "-g", "11211", "memcache"])
            .with_exec(["adduser", "-D", "-u", "1121", "-G", "memcache", "memcache"])
            .with_exec(["apk", "add", "--no-cache", "libsasl"])
            .with_env_variable("MEMCACHED_VERSION", "1.6.17")
            .with_env_variable(
                "MEMCACHED_SHA1",
                "e25639473e15f1bd9516b915fb7e03ab8209030f",
            )
        )

        # add dependencies to the container
        memcached = set_dependencies(memcached)

        # add source code to the container
        memcached = download_memcached(memcached)

        # build the application
        memcached = build_memcached(memcached)

        # set the container entrypoint
        memcached = (
            memcached.with_file(
                "/usr/local/bin/docker-entrypoint.sh",
                client.host().directory(".").file("docker-entrypoint.sh"),
            )
            .with_exec(
                [
                    "ln",
                    "-s",
                    "usr/local/bin/docker-entrypoint.sh",
                    "/entrypoint.sh",  # backwards compat
                ]
            )
            .with_entrypoint(["docker-entrypoint.sh"])
            .with_user("memcache")
            .with_default_args(["memcached"])
        )

        # publish the container image
        addr = await memcached.publish(PUBLISH_ADDRESS)

    print(f"Published to {addr}")


def set_dependencies(container: dagger.Container) -> dagger.Container:
    return container.with_exec(
        [
            "apk",
            "add",
            "--no-cache",
            "--virtual",
            ".build-deps",
            "ca-certificates",
            "coreutils",
            "cyrus-sasl-dev",
            "gcc",
            "libc-dev",
            "libevent-dev",
            "linux-headers",
            "make",
            "openssl",
            "openssl-dev",
            "perl",
            "perl-io-socket-ssl",
            "perl-utils",
        ]
    )


def download_memcached(container: dagger.Container) -> dagger.Container:
    url = "https://memcached.org/files/memcached-$MEMCACHED_VERSION.tar.gz"

    return (
        container.with_exec(["sh", "-c", f"wget -O memcached.tar.gz {url}"])
        .with_exec(
            ["sh", "-c", 'echo "$MEMCACHED_SHA1  memcached.tar.gz" | sha1sum -c -']
        )
        .with_exec(["mkdir", "-p", "/usr/src/memcached"])
        .with_exec(
            [
                "tar",
                "-xvf",
                "memcached.tar.gz",
                "-C",
                "/usr/src/memcached",
                "--strip-components=1",
            ]
        )
        .with_exec(["rm", "memcached.tar.gz"])
    )


def build_memcached(container: dagger.Container) -> dagger.Container:
    return (
        container.with_workdir("/usr/src/memcached")
        .with_exec(
            [
                "./configure",
                f"--build={GNU_ARCH}",
                "--enable-extstore",
                "--enable-sasl",
                "--enable-sasl-pwdb",
                "--enable-tls",
            ]
        )
        .with_exec(["make", "-j", NPROC])
        .with_exec(["make", "test", f"PARALLEL={NPROC}"])
        .with_exec(["make", "install"])
        .with_workdir("/usr/src/memcached")
        .with_exec(["rm", "-rf", "/usr/src/memcached"])
        .with_exec(
            [
                "sh",
                "-c",
                "apk add --no-network --virtual .memcached-rundeps $( scanelf --needed --nobanner --format '%n#p' --recursive /usr/local | tr ',' '\n' | sort -u | awk 'system(\"[ -e /usr/local/lib/\" $1 \" ]\") == 0 { next } { print \"so:\" $1 }')",
            ]
        )
        .with_exec(["apk", "del", "--no-network", ".build-deps"])
        .with_exec(["memcached", "-V"])
    )


anyio.run(main)
