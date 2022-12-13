package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

const (
	nproc   = "1"
	gnuArch = "arm64"
)

func main() {
	tag := os.Getenv("DOCKER_TAG")
	ctx := context.Background()

	// create a Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	memcached := client.Container().
		From("alpine:3.17").
		WithExec([]string{"addgroup", "-g", "11211", "memcache"}).
		WithExec([]string{"adduser", "-D", "-u", "1121", "-G", "memcache", "memcache"}).
		WithExec([]string{"apk", "add", "--no-cache", "libsasl"}).
		WithEnvVariable("MEMCACHED_VERSION", "1.6.17").
		WithEnvVariable("MEMCACHED_SHA1", "e25639473e15f1bd9516b915fb7e03ab8209030f")
	memcached = setDependencies(memcached)
	memcached = downloadMemcached(memcached)
	memcached = buildMemcached(memcached)

	entrypoint := client.Host().Directory(".").File("docker-entrypoint.sh")
	memcached = memcached.
		WithFile("/usr/local/bin/docker-entrypoint.sh", entrypoint).
		WithExec([]string{"ln", "-s", "usr/local/bin/docker-entrypoint.sh", "/entrypoint.sh"}).
		WithEntrypoint([]string{"docker-entrypoint.sh"}).
		WithUser("memcache")

	addr, err := memcached.Publish(ctx, tag)
	if err != nil {
		panic(err)
	}
	fmt.Printf("published to %s", addr)
}

func setDependencies(container *dagger.Container) *dagger.Container {
	return container.
		WithExec([]string{
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
		})
}

func downloadMemcached(container *dagger.Container) *dagger.Container {
	return container.
		WithExec([]string{"sh", "-c", "wget -O memcached.tar.gz https://memcached.org/files/memcached-$MEMCACHED_VERSION.tar.gz"}).
		WithExec([]string{"sh", "-c", "echo \"$MEMCACHED_SHA1  memcached.tar.gz\" | sha1sum -c -"}).
		WithExec([]string{"mkdir", "-p", "/usr/src/memcached"}).
		WithExec([]string{"tar", "-xvf", "memcached.tar.gz", "-C", "/usr/src/memcached", "--strip-components=1"}).
		WithExec([]string{"rm", "memcached.tar.gz"})
}

func buildMemcached(container *dagger.Container) *dagger.Container {
	return container.
		WithWorkdir("/usr/src/memcached").
		WithExec([]string{
			"./configure",
			fmt.Sprintf("--build=%s", gnuArch),
			"--enable-extstore",
			"--enable-sasl",
			"--enable-sasl-pwdb",
			"--enable-tls",
		}).
		WithExec([]string{"make", "-j", nproc}).
		WithExec([]string{"make", "test", fmt.Sprintf("PARALLEL=%s", nproc)}).
		WithExec([]string{"make", "install"}).
		WithWorkdir("/usr/src/memcached").
		WithExec([]string{"rm", "-rf", "/usr/src/memcached"}).
		WithExec([]string{
			"sh",
			"-c",
			"apk add --no-network --virtual .memcached-rundeps $( scanelf --needed --nobanner --format '%n#p' --recursive /usr/local | tr ',' '\n' | sort -u | awk 'system(\"[ -e /usr/local/lib/\" $1 \" ]\") == 0 { next } { print \"so:\" $1 }')",
		}).
		WithExec([]string{"apk", "del", "--no-network", ".build-deps"}).
		WithExec([]string{"memcached", "-V"})
}
