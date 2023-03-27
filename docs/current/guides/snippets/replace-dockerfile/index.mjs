import { connect } from "@dagger.io/dagger"


const NPROC = "1"
const GNU_ARCH = "arm64"
const PUBLISH_ADDRESS = "DOCKER-HUB-USERNAME/my-memcached"


connect(async (client) => {
    // set the base container
    // set environment variables
    let memcached = client.container()
        .from("alpine:3.17")
        .withExec(["addgroup", "-g", "11211", "memcache"])
        .withExec(["adduser", "-D", "-u", "1121", "-G", "memcache", "memcache"])
        .withExec(["apk", "add", "--no-cache", "libsasl"])
        .withEnvVariable("MEMCACHED_VERSION", "1.6.17")
        .withEnvVariable(
            "MEMCACHED_SHA1",
            "e25639473e15f1bd9516b915fb7e03ab8209030f",
        )

    // add dependencies to the container
    memcached = setDependencies(memcached)

    // add source code to the container
    memcached = downloadMemcached(memcached)

    // build the application
    memcached = buildMemcached(memcached)

    // set the container entrypoint
    memcached = memcached
        .withFile(
            "/usr/local/bin/docker-entrypoint.sh",
            client.host().directory(".").file("docker-entrypoint.sh"),
        )
        .withExec(
            [
                "ln",
                "-s",
                "usr/local/bin/docker-entrypoint.sh",
                "/entrypoint.sh",  // backwards compat
            ]
        )
        .withEntrypoint(["docker-entrypoint.sh"])
        .withUser("memcache")
        .withDefaultArgs({args: ["memcached"]})

    // publish the container image
    const addr = await memcached.publish(PUBLISH_ADDRESS)

    console.log(`Published to ${addr}`)

}, { LogOutput: process.stdout })


function setDependencies(container) {
    return container.withExec(
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
}

function downloadMemcached(container) {
    const url = "https://memcached.org/files/memcached-$MEMCACHED_VERSION.tar.gz"

    return container
        .withExec(["sh", "-c", `wget -O memcached.tar.gz ${url}`])
        .withExec(
            ["sh", "-c", 'echo "$MEMCACHED_SHA1  memcached.tar.gz" | sha1sum -c -']
        )
        .withExec(["mkdir", "-p", "/usr/src/memcached"])
        .withExec(
            [
                "tar",
                "-xvf",
                "memcached.tar.gz",
                "-C",
                "/usr/src/memcached",
                "--strip-components=1",
            ]
        )
        .withExec(["rm", "memcached.tar.gz"])
}

function buildMemcached(container) {
    return container
        .withWorkdir("/usr/src/memcached")
        .withExec(
            [
                "./configure",
                `--build=${GNU_ARCH}`,
                "--enable-extstore",
                "--enable-sasl",
                "--enable-sasl-pwdb",
                "--enable-tls",
            ]
        )
        .withExec(["make", "-j", NPROC])
        .withExec(["make", "test", `PARALLEL=${NPROC}`])
        .withExec(["make", "install"])
        .withWorkdir("/usr/src/memcached")
        .withExec(["rm", "-rf", "/usr/src/memcached"])
        .withExec(
            [
                "sh",
                "-c",
                "apk add --no-network --virtual .memcached-rundeps $( scanelf --needed --nobanner --format '%n#p' --recursive /usr/local | tr ',' '\n' | sort -u | awk 'system(\"[ -e /usr/local/lib/\" $1 \" ]\") == 0 { next } { print \"so:\" $1 }')",
            ]
        )
        .withExec(["apk", "del", "--no-network", ".build-deps"])
        .withExec(["memcached", "-V"])
}
