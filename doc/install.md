# Installing Dagger

1. Build the `dagger` command-line tool. You will need [Go](https://golang.org) version 1.16 or later.

```
$ make
```

2. Copy the `dagger` tool to a location listed in your `$PATH`. For example, to copy it to `/usr/local/bin`:

```
$ cp ./cmd/dagger/dagger /usr/local/bin
```

3. Make sure your machine is running a buildkit daemon. If you have Docker installed, you're already good to go.
Otherwise, install either [Docker](https://www.docker.com/products/docker-desktop) or [buildkit](https://github.com/moby/buildkit#quick-start).
