# syntax=docker/dockerfile-upstream:master

ARG GO_VERSION=1.21
ARG ALPINE_VERSION=3.19
ARG DEBIAN_VERSION=trixie

FROM debian:${DEBIAN_VERSION}-slim AS base
RUN apt-get update && apt-get --no-install-recommends install -y git binutils \
  gcc-x86-64-linux-gnu \
  binutils-x86-64-linux-gnu \
  binutils-arm-linux-gnueabihf \
  binutils-aarch64-linux-gnu \
  binutils-i686-linux-gnu \
  binutils-riscv64-linux-gnu \
  binutils-s390x-linux-gnu \
  binutils-powerpc64le-linux-gnu \
  binutils-mips64el-linux-gnuabi64 \
  binutils-mips64-linux-gnuabi64 \
  binutils-loongarch64-linux-gnu
WORKDIR /src

FROM base AS exit-amd64
COPY util/archutil/fixtures/exit.amd64.S .
RUN x86_64-linux-gnu-gcc -static -nostdlib -Wa,--noexecstack -o exit exit.amd64.S && x86_64-linux-gnu-strip --strip-unneeded exit

FROM base AS exit-386
COPY util/archutil/fixtures/exit.386.s .
RUN i686-linux-gnu-as --noexecstack -o exit.o exit.386.s && i686-linux-gnu-ld -o exit -s exit.o && i686-linux-gnu-strip --strip-unneeded exit

FROM base AS exit-arm64
COPY util/archutil/fixtures/exit.arm64.s .
RUN aarch64-linux-gnu-as --noexecstack -o exit.o exit.arm64.s && aarch64-linux-gnu-ld -o exit -s exit.o && aarch64-linux-gnu-strip --strip-unneeded exit

FROM base AS exit-arm
COPY util/archutil/fixtures/exit.arm.s .
RUN arm-linux-gnueabihf-as --noexecstack -o exit.o exit.arm.s && arm-linux-gnueabihf-ld -o exit -s exit.o && arm-linux-gnueabihf-strip --strip-unneeded exit

FROM base AS exit-riscv64
COPY util/archutil/fixtures/exit.riscv64.s .
RUN riscv64-linux-gnu-as --noexecstack -o exit.o exit.riscv64.s && riscv64-linux-gnu-ld -o exit -s exit.o && riscv64-linux-gnu-strip --strip-unneeded exit

FROM base AS exit-s390x
COPY util/archutil/fixtures/exit.s390x.s .
RUN s390x-linux-gnu-as --noexecstack -o exit.o exit.s390x.s && s390x-linux-gnu-ld -o exit -s exit.o && s390x-linux-gnu-strip --strip-unneeded exit

FROM base AS exit-ppc64
COPY util/archutil/fixtures/exit.ppc64.s .
RUN powerpc64le-linux-gnu-as -mbig --noexecstack -o exit.o exit.ppc64.s && powerpc64le-linux-gnu-ld -EB -o exit -s exit.o && powerpc64le-linux-gnu-strip --strip-unneeded exit

FROM base AS exit-ppc64le
COPY util/archutil/fixtures/exit.ppc64le.s .
RUN powerpc64le-linux-gnu-as --noexecstack -o exit.o exit.ppc64le.s && powerpc64le-linux-gnu-ld -o exit -s exit.o && powerpc64le-linux-gnu-strip --strip-unneeded exit

FROM base AS exit-mips64le
COPY util/archutil/fixtures/exit.mips64le.s .
RUN mips64el-linux-gnuabi64-as --noexecstack -o exit.o exit.mips64le.s && mips64el-linux-gnuabi64-ld -o exit -s exit.o && mips64el-linux-gnuabi64-strip --strip-unneeded exit

FROM base AS exit-mips64
COPY util/archutil/fixtures/exit.mips64.s .
RUN mips64-linux-gnuabi64-as --noexecstack -o exit.o exit.mips64.s && mips64-linux-gnuabi64-ld -o exit -s exit.o && mips64-linux-gnuabi64-strip --strip-unneeded exit

FROM base AS exit-loong64
COPY util/archutil/fixtures/exit.loongarch64.s .
RUN loongarch64-linux-gnu-as --noexecstack -o exit.o exit.loongarch64.s && loongarch64-linux-gnu-ld -o exit -s exit.o && loongarch64-linux-gnu-strip --strip-unneeded exit

FROM scratch AS exits
COPY --from=exit-amd64 /src/exit amd64
COPY --from=exit-386 /src/exit 386
COPY --from=exit-arm64 /src/exit arm64
COPY --from=exit-arm /src/exit arm
COPY --from=exit-riscv64 /src/exit riscv64
COPY --from=exit-s390x /src/exit s390x
COPY --from=exit-ppc64 /src/exit ppc64
COPY --from=exit-ppc64le /src/exit ppc64le
COPY --from=exit-mips64le /src/exit mips64le
COPY --from=exit-mips64 /src/exit mips64
COPY --from=exit-loong64 /src/exit loong64

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS generate
WORKDIR /go/src/github.com/moby/buildkit
RUN --mount=type=bind,target=.,rw \
    --mount=from=exits,target=./bin/archutil,rw <<EOT
  set -ex
  mkdir /out
  go run ./util/archutil/generate.go \
    bin/archutil/amd64 \
    bin/archutil/386 \
    bin/archutil/arm64 \
    bin/archutil/arm \
    bin/archutil/riscv64 \
    bin/archutil/s390x \
    bin/archutil/ppc64 \
    bin/archutil/ppc64le \
    bin/archutil/mips64le \
    bin/archutil/mips64 \
    bin/archutil/loong64
  tree -nh bin/archutil
  cp bin/archutil/*_binary.go /out
EOT

FROM scratch AS update
COPY --from=generate /out /

FROM base AS validate
RUN --mount=type=bind,target=.,rw \
    --mount=type=bind,from=generate,source=/out,target=/generated-files <<EOT
  set -e
  git add -A
  if [ "$(ls -A /generated-files)" ]; then
    cp -rf /generated-files/* ./util/archutil
  fi
  diff=$(git status --porcelain)
  if [ -n "$diff" ]; then
    echo >&2 'ERROR: The result of archutil differs. Please update with "make archutil"'
    echo "$diff"
    exit 1
  fi
EOT
