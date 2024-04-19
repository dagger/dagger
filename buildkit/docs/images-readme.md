# BuildKit

BuildKit is a concurrent, cache-efficient, and Dockerfile-agnostic builder toolkit.

Report issues at https://github.com/moby/buildkit

Join `#buildkit` channel on [Docker Community Slack](https://dockr.ly/comm-slack)

# Tags

### Latest stable release

- [`v0.10.0`, `latest`](https://github.com/moby/buildkit/blob/v0.10.0/Dockerfile)

- [`v0.10.0-rootless`, `rootless`](https://github.com/moby/buildkit/blob/v0.10.0/Dockerfile) (see [`docs/rootless.md`](https://github.com/moby/buildkit/blob/master/docs/rootless.md) for usage)

### Development build from master branch

- [`master`](https://github.com/moby/buildkit/blob/master/Dockerfile)

- [`master-rootless`](https://github.com/moby/buildkit/blob/master/Dockerfile)


Binary releases and changelog can be found from https://github.com/moby/buildkit/releases

# Usage


To run daemon in a container:

```bash
docker run -d --name buildkitd --privileged moby/buildkit:latest
export BUILDKIT_HOST=docker-container://buildkitd
buildctl build --help
```

See https://github.com/moby/buildkit#buildkit for general BuildKit usage instructions


## Docker Buildx

[Buildx](https://github.com/docker/buildx) uses the latest stable image by default. To set a custom BuildKit image version use `--driver-opt`:

```bash
docker buildx create --driver-opt image=moby/buildkit:master --use
```


## Rootless

For Rootless deployments, see [`docs/rootless.md`](https://github.com/moby/buildkit/blob/master/docs/rootless.md)


## Kubernetes

For Kubernetes deployments, see [`examples/kubernetes`](https://github.com/moby/buildkit/tree/master/examples/kubernetes)


## Daemonless

To run the client and an ephemeral daemon in a single container ("daemonless mode"):

```bash
docker run \
    -it \
    --rm \
    --privileged \
    -v /path/to/dir:/tmp/work \
    --entrypoint buildctl-daemonless.sh \
    moby/buildkit:master \
        build \
        --frontend dockerfile.v0 \
        --local context=/tmp/work \
        --local dockerfile=/tmp/work
```

Rootless mode:

```bash
docker run \
    -it \
    --rm \
    --security-opt seccomp=unconfined \
    --security-opt apparmor=unconfined \
    -e BUILDKITD_FLAGS=--oci-worker-no-process-sandbox \
    -v /path/to/dir:/tmp/work \
    --entrypoint buildctl-daemonless.sh \
    moby/buildkit:master-rootless \
        build \
        --frontend \
        dockerfile.v0 \
        --local context=/tmp/work \
        --local dockerfile=/tmp/work
```
