# syntax=docker/dockerfile-upstream:master

ARG NODE_VERSION=19

FROM node:${NODE_VERSION}-alpine AS base
RUN apk add --no-cache git
WORKDIR /src

FROM base AS doctoc
RUN npm install -g doctoc
RUN --mount=type=bind,source=README.md,target=README.md,rw <<EOT
  set -e
  doctoc README.md
  mkdir /out
  cp README.md /out/
EOT

FROM scratch AS update
COPY --from=doctoc /out /

FROM base AS validate-toc
RUN --mount=type=bind,target=.,rw \
    --mount=type=bind,from=doctoc,source=/out/README.md,target=./README.md <<EOT
  set -e
  diff=$(git status --porcelain -- 'README.md')
  if [ -n "$diff" ]; then
    echo >&2 'ERROR: The result of "doctoc" differs. Please update with "make doctoc"'
    echo "$diff"
    exit 1
  fi
EOT
