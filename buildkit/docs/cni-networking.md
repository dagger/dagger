# CNI networking

It can be useful to use a bridge network for your builder if for example you
encounter a network port contention during multiple builds. If you're using
the BuildKit image, CNI is not [(yet)](https://github.com/moby/buildkit/issues/28)
available in it.

But you can create your own BuildKit image with CNI support:

```dockerfile
ARG BUILDKIT_VERSION=v0.9.3
ARG CNI_VERSION=v1.0.1

FROM --platform=$BUILDPLATFORM alpine AS cni-plugins
RUN apk add --no-cache curl
ARG CNI_VERSION
ARG TARGETOS
ARG TARGETARCH
WORKDIR /opt/cni/bin
RUN curl -Ls https://github.com/containernetworking/plugins/releases/download/$CNI_VERSION/cni-plugins-$TARGETOS-$TARGETARCH-$CNI_VERSION.tgz | tar xzv

FROM moby/buildkit:${BUILDKIT_VERSION}
ARG BUILDKIT_VERSION
RUN apk add --no-cache iptables
COPY --from=cni-plugins /opt/cni/bin /opt/cni/bin
ADD https://raw.githubusercontent.com/moby/buildkit/${BUILDKIT_VERSION}/hack/fixtures/cni.json /etc/buildkit/cni.json
```

Here we use the [CNI config for integration tests in BuildKit](../hack/fixtures/cni.json),
but feel free to use your own config.
