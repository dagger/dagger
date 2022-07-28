# syntax = docker/dockerfile:1

FROM node:16-alpine AS build
ENV YARN_CACHE_FOLDER /cache/yarn
WORKDIR /app/src
RUN apk add --no-cache file git
COPY --link examples/netlify/ts/package.json examples/netlify/ts/yarn.lock ./
COPY --link sdk/nodejs /sdk/nodejs
RUN --mount=type=cache,target=/cache/yarn,id=yarn,sharing=locked yarn --cwd /sdk/nodejs/dagger
RUN --mount=type=cache,target=/cache/yarn,id=yarn,sharing=locked yarn --cwd /sdk/nodejs/dagger build
RUN --mount=type=cache,target=/cache/yarn,id=yarn,sharing=locked yarn
COPY --link examples/netlify/ts/ .
RUN --mount=type=cache,target=/cache/yarn,id=yarn,sharing=locked yarn upgrade dagger
RUN --mount=type=cache,target=/cache/yarn,id=yarn,sharing=locked yarn build

FROM node:16-alpine
ENV YARN_CACHE_FOLDER /cache/yarn
WORKDIR /app/src
COPY --link --from=build /app/src/package.json /app/src/yarn.lock ./
COPY --link --from=build /sdk/nodejs /sdk/nodejs
RUN --mount=type=cache,target=/cache/yarn,id=yarn,sharing=locked yarn --production
COPY --link --from=build /app/src/dist ./dist
COPY --link examples/netlify/ts/schema.graphql /schema.graphql
COPY --link examples/netlify/ts/operations.graphql /operations.graphql
ENV PATH $PATH:/app/src/node_modules/.bin
COPY <<EOF /entrypoint
#!/bin/sh
node --unhandled-rejections=strict /app/src/dist/index.js
EOF
RUN chmod +x /entrypoint
ENTRYPOINT ["/entrypoint"]
