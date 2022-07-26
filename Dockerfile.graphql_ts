# syntax = docker/dockerfile:1

FROM node:16-alpine AS build
WORKDIR /app/src
RUN apk add --no-cache file git
COPY --link examples/graphql_ts/package.json examples/graphql_ts/yarn.lock ./
COPY --link sdk/nodejs /sdk/nodejs
RUN yarn --cwd /sdk/nodejs/dagger build
RUN yarn
COPY --link examples/graphql_ts/ .
RUN yarn upgrade dagger
RUN yarn build

FROM node:16-alpine
WORKDIR /app/src
COPY --link --from=build /app/src/package.json /app/src/yarn.lock ./
COPY --link --from=build /sdk/nodejs /sdk/nodejs
RUN yarn --production
COPY --link --from=build /app/src/dist ./dist
COPY --link examples/graphql_ts/entrypoint.sh /entrypoint
COPY --link examples/graphql_ts/schema.graphql /schema.graphql
ENTRYPOINT ["/entrypoint"]
LABEL moby.buildkit.frontend.caps=moby.buildkit.frontend.inputs
