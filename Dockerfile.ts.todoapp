# syntax = docker/dockerfile:1

FROM node:16-alpine AS build
ENV YARN_CACHE_FOLDER /cache/yarn
WORKDIR /app/src
RUN apk add --no-cache file git
COPY --link examples/todoapp/ts/package.json examples/todoapp/ts/yarn.lock ./
COPY --link sdk/nodejs /sdk/nodejs
RUN --mount=type=cache,target=/cache/yarn,id=yarn yarn --cwd /sdk/nodejs/dagger
RUN --mount=type=cache,target=/cache/yarn,id=yarn yarn --cwd /sdk/nodejs/dagger build
RUN --mount=type=cache,target=/cache/yarn,id=yarn yarn
COPY --link examples/todoapp/ts/ .
RUN --mount=type=cache,target=/cache/yarn,id=yarn yarn upgrade dagger
RUN --mount=type=cache,target=/cache/yarn,id=yarn yarn build

FROM node:16-alpine
ENV YARN_CACHE_FOLDER /cache/yarn
WORKDIR /app/src
COPY --link --from=build /app/src/package.json /app/src/yarn.lock ./
COPY --link --from=build /sdk/nodejs /sdk/nodejs
RUN --mount=type=cache,target=/cache/yarn,id=yarn yarn --production
COPY --link --from=build /app/src/dist ./dist
COPY --link examples/todoapp/ts/schema.graphql /schema.graphql
COPY --link examples/todoapp/ts/operations.graphql /operations.graphql
ENV PATH $PATH:/app/src/node_modules/.bin
COPY <<EOF /entrypoint
#!/bin/sh
node --unhandled-rejections=strict /app/src/dist/index.js
EOF
RUN chmod +x /entrypoint
ENTRYPOINT /entrypoint
