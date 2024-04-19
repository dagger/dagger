# syntax=docker/dockerfile-upstream:master

FROM mvdan/shfmt:v3.1.2-alpine AS shfmt
WORKDIR /src
ARG SHFMT_FLAGS="-i 2 -ci"

FROM shfmt AS generate
WORKDIR /out/hack
RUN --mount=target=/src \
  cp -a /src/hack/. ./ && \
  shfmt -l -w $SHFMT_FLAGS .

FROM scratch AS update
COPY --from=generate /out /

FROM shfmt AS validate
RUN --mount=target=. \
  shfmt $SHFMT_FLAGS -d ./hack
