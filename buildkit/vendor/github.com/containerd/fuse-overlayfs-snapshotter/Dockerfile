#   Copyright The containerd Authors.

#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at

#       http://www.apache.org/licenses/LICENSE-2.0

#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.

ARG FUSEOVERLAYFS_COMMIT=main
ARG ROOTLESSKIT_COMMIT=v1.1.0

ARG GO_VERSION=1.20
ARG DEBIAN_VERSION=11
ARG ALPINE_VERSION=3.18

FROM golang:${GO_VERSION}-alpine AS containerd-fuse-overlayfs-test
COPY . /go/src/github.com/containerd/fuse-overlayfs-snapshotter
WORKDIR  /go/src/github.com/containerd/fuse-overlayfs-snapshotter
ENV CGO_ENABLED=0
ENV GO111MODULE=on
RUN mkdir /out && go test -c -o /out/containerd-fuse-overlayfs.test

# from https://github.com/containers/fuse-overlayfs/blob/53c17dab78b43de1cd121bf9260b20b76371bbaf/Dockerfile.static.ubuntu
FROM debian:${DEBIAN_VERSION} AS fuse-overlayfs
RUN apt-get update && \
    apt-get install --no-install-recommends -y \
        git ca-certificates libc6-dev gcc g++ make automake autoconf clang pkgconf libfuse3-dev
RUN git clone https://github.com/containers/fuse-overlayfs
WORKDIR fuse-overlayfs
ARG FUSEOVERLAYFS_COMMIT
RUN git pull && git checkout ${FUSEOVERLAYFS_COMMIT}
RUN  ./autogen.sh && \
     LIBS="-ldl" LDFLAGS="-static" ./configure && \
     make && mkdir /out && cp fuse-overlayfs /out

FROM golang:${GO_VERSION}-alpine AS rootlesskit
RUN apk add --no-cache git
RUN git clone https://github.com/rootless-containers/rootlesskit.git /go/src/github.com/rootless-containers/rootlesskit
WORKDIR /go/src/github.com/rootless-containers/rootlesskit
ARG ROOTLESSKIT_COMMIT
RUN git pull && git checkout ${ROOTLESSKIT_COMMIT}
ENV CGO_ENABLED=0
RUN mkdir /out && go build -o /out/rootlesskit github.com/rootless-containers/rootlesskit/cmd/rootlesskit 

FROM alpine:${ALPINE_VERSION}
COPY --from=containerd-fuse-overlayfs-test /out/containerd-fuse-overlayfs.test /usr/local/bin
COPY --from=rootlesskit /out/rootlesskit /usr/local/bin
COPY --from=fuse-overlayfs /out/fuse-overlayfs /usr/local/bin
RUN apk add --no-cache fuse3 libcap shadow-uidmap && \
    setcap CAP_SETUID=ep /usr/bin/newuidmap && \
    setcap CAP_SETGID=ep /usr/bin/newgidmap && \
    adduser -D -u 1000 testuser && \
    echo testuser:100000:65536 | tee /etc/subuid | tee /etc/subgid
USER testuser
# If /tmp is real overlayfs, some tests fail. Mount a volume to ensure /tmp to be a sane filesystem.
VOLUME /tmp
# requires --security-opt seccomp=unconfined --security-opt apparmor=unconfined --device /dev/fuse 
CMD ["rootlesskit", "containerd-fuse-overlayfs.test", "-test.root", "-test.v"]
