## Nydus image formats

Nydus is an OCI/Docker-compatible accelerated image format provided by the Dragonfly [image-service](https://github.com/dragonflyoss/image-service) project, which offers the ability to pull image data on-demand, without waiting for the entire image pull to complete and then start the container. It has been put in production usage and shown vast improvements to significantly reduce the overhead costs on time, network, disk IO of pulling image or starting container.

Nydus image can be flexibly configured as a FUSE-based user-space filesystem or in-kernel [EROFS](https://www.kernel.org/doc/html/latest/filesystems/erofs.html) (from Linux kernel v5.16) with nydus daemon in user-space, integrating with VM-based container runtime like [KataContainers](https://katacontainers.io/) is much easier.

## Creating Nydus images

### Buildkitd with Nydus Support

To enable buildkit support for Nydus image export, we need to build `buildkitd` with the following command:

```
go build -tags=nydus -o ./bin/buildkitd ./cmd/buildkitd
```

### Building Nydus with BuildKit

Download `nydus-image` binary from [nydus release page](https://github.com/dragonflyoss/image-service/releases) (require v2.1.6 or higher), then put the `nydus-image` binary path into $PATH or specifying it on `NYDUS_BUILDER` environment variable for buildkitd:

```
env NYDUS_BUILDER=/path/to/nydus-image buildkitd ...
```

Note: some nydus intermediate files will be created in the working directory during the build process, which will be cleaned up automatically after the build is completed. Use the `NYDUS_WORKDIR` environment variable to change this working directory.

On buildctl side, export nydus image as one of the compression types by specifying `compression=nydus,force-compression=true` options:

```
buildctl build ... \
  --output type=image,name=docker.io/username/image,push=true,compression=nydus,force-compression=true,oci-mediatypes=true
```

### Known limitations

- The export of Nydus image and runtime (e.g. [docker](https://github.com/dragonflyoss/image-service/tree/master/contrib/docker-nydus-graphdriver), [containerd](https://github.com/containerd/nydus-snapshotter), etc.) is currently only supported on linux platform.
- Nydus image layers cannot be mixed with other compression types in the same image, so the `force-compression=true` option must be enabled when exporting both Nydus and other compression types.
- Specifying a Nydus image as a base image in a Dockerfile is supported, but it does not currently support lazy pulling.
- Since exported Nydus image will always have one more metadata layer than images in other compression types, Nydus image cannot be exported/imported as cache.

### Other ways to create Nydus images

Pre-converted nydus images are available at [`ghcr.io/dragonflyoss/image-service` repository](https://github.com/orgs/dragonflyoss/packages?ecosystem=container) (mainly for testing purpose).

[`Nydusify`](https://github.com/dragonflyoss/image-service/blob/master/docs/nydusify.md) The Nydusify CLI tool pulls & converts an OCIv1 image into a nydus image, and pushes nydus image to registry.

[`Harbor Acceld`](https://github.com/goharbor/acceleration-service) Harbor acceld provides a general service to convert OCIv1 image to acceleration image like [Nydus](https://github.com/dragonflyoss/image-service) and [eStargz](https://github.com/containerd/stargz-snapshotter) etc.
