# Build reproducibility

## Reproducing the pinned dependencies

Reproducing the pinned dependencies is supported since BuildKit v0.11.

e.g.,
```bash
buildctl build --frontend dockerfile.v0 --local dockerfile=. --local context=. --source-policy-file policy.json
```

An example `policy.json`:
```json
{
  "rules": [
    {
      "action": "CONVERT",
      "selector": {
        "identifier": "docker-image://docker.io/library/alpine:latest"
      },
      "updates": {
        "identifier": "docker-image://docker.io/library/alpine:latest@sha256:4edbd2beb5f78b1014028f4fbb99f3237d9561100b6881aabbf5acce2c4f9454"
      }
    },
    {
      "action": "CONVERT",
      "selector": {
        "identifier": "https://raw.githubusercontent.com/moby/buildkit/v0.10.1/README.md"
      },
      "updates": {
        "attrs": {"http.checksum": "sha256:6e4b94fc270e708e1068be28bd3551dc6917a4fc5a61293d51bb36e6b75c4b53"}
      }
    }
  ]
}
```

Any source type is supported, but how to pin a source depends on the type.

## `SOURCE_DATE_EPOCH`
[`SOURCE_DATE_EPOCH`](https://reproducible-builds.org/docs/source-date-epoch/) is the convention for pinning timestamps to a specific value.

The Dockerfile frontend supports consuming the `SOURCE_DATE_EPOCH` value as a special build arg, since BuildKit 0.11.
Minimal support is also available on older BuildKit when using Dockerfile 1.5 frontend.

```console
buildctl build --frontend dockerfile.v0 --opt build-arg:SOURCE_DATE_EPOCH=$(git log -1 --pretty=%ct) ...
```
The `buildctl` CLI (>= v0.13) and Docker Buildx (>= 0.10) automatically propagate the `$SOURCE_DATE_EPOCH` environment value from the client host to the `SOURCE_DATE_EPOCH` build arg.

The build arg value is used for:
- the `created` timestamp in the [OCI Image Config](https://github.com/opencontainers/image-spec/blob/main/config.md#properties)
- the `created` timestamp in the `history` objects in the [OCI Image Config](https://github.com/opencontainers/image-spec/blob/main/config.md#properties)
- the `org.opencontainers.image.created` annotation in the [OCI Image Index](https://github.com/opencontainers/image-spec/blob/main/annotations.md#pre-defined-annotation-keys)
- the timestamp of the files exported with the `local` exporter
- the timestamp of the files exported with the `tar` exporter

To apply the build arg value to the timestamps of the files inside the image, specify `rewrite-timestamp=true` as an image exporter option:
```
--output type=image,name=docker.io/username/image,push=true,rewrite-timestamp=true
```

The `rewrite-timestamp` option is available since BuildKit v0.13.
See [v0.12 documentation](https://github.com/moby/buildkit/blob/v0.12/docs/build-repro.md#caveats) for dealing with timestamps
in BuildKit v0.12 and v0.11.

See also the [documentation](/frontend/dockerfile/docs/reference.md#buildkit-built-in-build-args) of the Dockerfile frontend.
